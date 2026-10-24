// Package store persists parsed messages to SQLite.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite" // Registers the "sqlite" driver.

	"github.com/JanneSaukkio/enron-mail-processor/internal/eml"
)

const schema = `
CREATE TABLE IF NOT EXISTS user (
    id INTEGER NOT NULL PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS email (
    id INTEGER NOT NULL PRIMARY KEY,
    message_id TEXT NOT NULL UNIQUE,
    date INTEGER NOT NULL,
    from_user_id INTEGER NOT NULL REFERENCES user (id),
    hash TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS recipient (
    email_id INTEGER NOT NULL REFERENCES email (id),
    user_id INTEGER NOT NULL REFERENCES user (id),
    type TEXT
);

CREATE INDEX IF NOT EXISTS email_from_user_id ON email (from_user_id);
`

const dropSchema = `
DROP TABLE IF EXISTS recipient;
DROP TABLE IF EXISTS email;
DROP TABLE IF EXISTS user;
`

// The name of an existing user is only filled in when it was unknown before,
// otherwise duplicates are ignored. RETURNING gives the id in both cases.
const upsertUser = `
INSERT INTO user (email, name) VALUES (?, ?)
ON CONFLICT (email) DO UPDATE SET name = CASE WHEN user.name = '' THEN excluded.name ELSE user.name END
RETURNING id`

const insertEmail = `INSERT OR IGNORE INTO email (message_id, date, from_user_id, hash) VALUES (?, ?, ?, ?)`

const insertRecipient = `INSERT INTO recipient (email_id, user_id, type) VALUES (?, ?, ?)`

// batchSize is the number of messages written per transaction. Committing
// every message separately would make ingestion of the full dataset slow.
const batchSize = 1000

// Store is a SQLite backed message store. SaveMessage and Flush must be
// called from a single goroutine; the query methods are safe for concurrent
// use.
type Store struct {
	db      *sql.DB
	tx      *sql.Tx
	pending int
}

// Sender is a user together with the number of messages they have sent.
type Sender struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	MessagesSent int64  `json:"messages_sent"`
}

// Stats holds totals of the stored data.
type Stats struct {
	Messages int64
	Users    int64
	Senders  int64
}

// Open opens or creates the database at path and initializes the schema.
// When clean is true, existing tables are dropped first.
func Open(ctx context.Context, path string, clean bool) (*Store, error) {
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if clean {
		if _, err := db.ExecContext(ctx, dropSchema); err != nil {
			db.Close()
			return nil, fmt.Errorf("drop schema: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close commits pending writes and closes the database.
func (s *Store) Close() error {
	return errors.Join(s.Flush(), s.db.Close())
}

// SaveMessage stores the users of msg and, unless it is a duplicate, the
// message and its recipients. It reports whether the message was inserted.
// A failed message is rolled back without affecting the rest of the batch.
func (s *Store) SaveMessage(ctx context.Context, msg *eml.Message) (inserted bool, err error) {
	// A cancelled context would roll back the whole batch. Writing a single
	// message is quick, so let it finish and leave stopping to the caller.
	ctx = context.WithoutCancel(ctx)

	if s.tx == nil {
		if s.tx, err = s.db.BeginTx(ctx, nil); err != nil {
			return false, fmt.Errorf("begin transaction: %w", err)
		}
	}

	if _, err := s.tx.ExecContext(ctx, "SAVEPOINT message"); err != nil {
		return false, fmt.Errorf("savepoint: %w", err)
	}
	inserted, err = s.saveMessage(ctx, msg)
	if err != nil {
		_, rbErr := s.tx.ExecContext(ctx, "ROLLBACK TO message; RELEASE message")
		return false, errors.Join(err, rbErr)
	}
	if _, err := s.tx.ExecContext(ctx, "RELEASE message"); err != nil {
		return false, fmt.Errorf("release savepoint: %w", err)
	}

	s.pending++
	if s.pending >= batchSize {
		if err := s.Flush(); err != nil {
			return false, err
		}
	}
	return inserted, nil
}

func (s *Store) saveMessage(ctx context.Context, msg *eml.Message) (bool, error) {
	fromID, err := s.userID(ctx, msg, msg.From)
	if err != nil {
		return false, err
	}

	type recipient struct {
		userID int64
		typ    sql.NullString
	}
	var recipients []recipient
	for _, group := range []struct {
		addrs []string
		typ   sql.NullString
	}{
		{msg.To, sql.NullString{}},
		{msg.Cc, sql.NullString{String: "cc", Valid: true}},
		{msg.Bcc, sql.NullString{String: "bcc", Valid: true}},
	} {
		for _, addr := range group.addrs {
			id, err := s.userID(ctx, msg, addr)
			if err != nil {
				return false, err
			}
			recipients = append(recipients, recipient{id, group.typ})
		}
	}

	res, err := s.tx.ExecContext(ctx, insertEmail, msg.MessageID, msg.Date.UnixMilli(), fromID, msg.Hash())
	if err != nil {
		return false, fmt.Errorf("insert email: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	emailID, err := res.LastInsertId()
	if err != nil {
		return false, fmt.Errorf("email id: %w", err)
	}

	for _, r := range recipients {
		if _, err := s.tx.ExecContext(ctx, insertRecipient, emailID, r.userID, r.typ); err != nil {
			return false, fmt.Errorf("insert recipient: %w", err)
		}
	}
	return true, nil
}

func (s *Store) userID(ctx context.Context, msg *eml.Message, addr string) (int64, error) {
	var id int64
	if err := s.tx.QueryRowContext(ctx, upsertUser, addr, msg.Name(addr)).Scan(&id); err != nil {
		return 0, fmt.Errorf("upsert user %q: %w", addr, err)
	}
	return id, nil
}

// Flush commits the current batch.
func (s *Store) Flush() error {
	if s.tx == nil {
		return nil
	}
	err := s.tx.Commit()
	s.tx = nil
	s.pending = 0
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// TopSenders returns senders ordered by the number of messages sent, and the
// total number of senders.
func (s *Store) TopSenders(ctx context.Context, limit, offset int) ([]Sender, int64, error) {
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT from_user_id) FROM email`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count senders: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.email, u.name, COUNT(*) AS sent
		FROM email e
		JOIN user u ON u.id = e.from_user_id
		GROUP BY u.id
		ORDER BY sent DESC, u.id
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("query top senders: %w", err)
	}
	defer rows.Close()

	senders := []Sender{}
	for rows.Next() {
		var sd Sender
		if err := rows.Scan(&sd.ID, &sd.Email, &sd.Name, &sd.MessagesSent); err != nil {
			return nil, 0, fmt.Errorf("scan sender: %w", err)
		}
		senders = append(senders, sd)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("read senders: %w", err)
	}
	return senders, total, nil
}

// Stats returns totals of the stored messages and users.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM email),
			(SELECT COUNT(*) FROM user),
			(SELECT COUNT(DISTINCT from_user_id) FROM email)`).
		Scan(&st.Messages, &st.Users, &st.Senders)
	if err != nil {
		return Stats{}, fmt.Errorf("query stats: %w", err)
	}
	return st, nil
}

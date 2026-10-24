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

CREATE INDEX IF NOT EXISTS email_from_user_id ON email (from_user_id);
`

const dropSchema = `
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

// batchSize is the number of messages written per transaction. Committing
// every message separately would make ingestion of the full dataset slow.
const batchSize = 1000

// Store is a SQLite backed message store. SaveMessage and Flush must be
// called from a single goroutine; the query methods are safe for concurrent
// use.
type Store struct {
	db      *sql.DB
	tx      *sql.Tx
	stmts   txStmts
	pending int

	// users caches user ids by email so that known users are not upserted
	// again for every message.
	users map[string]cachedUser
	// uncommitted lists the cache entries written in the current
	// transaction. They are dropped if the transaction or the message
	// savepoint is rolled back, since their rows no longer exist.
	uncommitted []string
}

// txStmts are the statements run for every message, prepared once per
// transaction to avoid parsing the SQL again for each row.
type txStmts struct {
	savepoint   *sql.Stmt
	release     *sql.Stmt
	upsertUser  *sql.Stmt
	insertEmail *sql.Stmt
}

type cachedUser struct {
	id int64
	// named tells whether a name has been stored. An unnamed user is
	// upserted again when a message carries their name.
	named bool
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

// Memory is the path that makes Open use an in-memory database. Its data is
// lost when the Store is closed.
const Memory = "memory"

// Open opens or creates the database at path and initializes the schema.
// When clean is true, existing tables are dropped first. When path is Memory,
// the database is kept in memory.
func Open(ctx context.Context, path string, clean bool) (*Store, error) {
	// The driver runs each _pragma on every new connection. That matters
	// because database/sql keeps a pool of connections and most pragmas only
	// apply to the connection they run on.
	//
	//   - journal_mode(WAL): readers and the writer don't block each other, so
	//     the API can serve queries while ingestion is writing. Unlike the
	//     other pragmas, this setting is stored in the database file.
	//   - synchronous(NORMAL): in WAL mode, fsync only happens at checkpoints
	//     instead of on every commit. A power loss can drop the latest
	//     commits but never corrupts the database; an application crash
	//     loses nothing. Lost commits can be recovered by ingesting again.
	//   - foreign_keys(1): SQLite ignores REFERENCES constraints unless this
	//     is enabled, so rows pointing to missing users or emails would be
	//     accepted silently.
	//   - busy_timeout(5000): a connection that finds the database locked,
	//     for example during a commit or checkpoint, waits up to 5 seconds
	//     instead of failing immediately with SQLITE_BUSY.
	//   - cache_size(-65536): a negative value is in KiB, so 64 MiB of page
	//     cache per connection instead of the default 2 MiB. With the default,
	//     a batch transaction outgrows the cache and SQLite writes pages to
	//     the WAL in the middle of it. The larger cache cut ingestion time of
	//     the performance test dataset by about 8%.
	file := path
	if path == Memory {
		file = ":memory:"
	}
	dsn := "file:" + file +
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)" +
		"&_pragma=cache_size(-65536)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if path == Memory {
		// Every connection to :memory: gets a separate, empty database, so
		// the pool is limited to one connection that is never closed. API
		// queries wait for the writer to commit its current batch.
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		db.SetConnMaxLifetime(0)
		db.SetConnMaxIdleTime(0)
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
	return &Store{db: db, users: make(map[string]cachedUser)}, nil
}

// Close commits pending writes and closes the database.
func (s *Store) Close() error {
	return errors.Join(s.Flush(), s.db.Close())
}

// SaveMessage stores the sender of msg and, unless it is a duplicate, the
// message. It reports whether the message was inserted.
// A failed message is rolled back without affecting the rest of the batch.
func (s *Store) SaveMessage(ctx context.Context, msg *eml.Message) (inserted bool, err error) {
	// A cancelled context would roll back the whole batch. Writing a single
	// message is quick, so let it finish and leave stopping to the caller.
	ctx = context.WithoutCancel(ctx)

	if s.tx == nil {
		if err := s.begin(ctx); err != nil {
			return false, err
		}
	}

	if _, err := s.stmts.savepoint.ExecContext(ctx); err != nil {
		return false, fmt.Errorf("savepoint: %w", err)
	}
	mark := len(s.uncommitted)
	inserted, err = s.saveMessage(ctx, msg)
	if err != nil {
		s.forgetUsers(mark)
		_, rbErr := s.tx.ExecContext(ctx, "ROLLBACK TO message; RELEASE message")
		return false, errors.Join(err, rbErr)
	}
	if _, err := s.stmts.release.ExecContext(ctx); err != nil {
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

// begin starts a transaction and prepares the per-message statements in it.
// The statements are closed when the transaction ends.
func (s *Store) begin(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	var stmts txStmts
	for _, p := range []struct {
		stmt  **sql.Stmt
		query string
	}{
		{&stmts.savepoint, "SAVEPOINT message"},
		{&stmts.release, "RELEASE message"},
		{&stmts.upsertUser, upsertUser},
		{&stmts.insertEmail, insertEmail},
	} {
		if *p.stmt, err = tx.PrepareContext(ctx, p.query); err != nil {
			return errors.Join(fmt.Errorf("prepare %q: %w", p.query, err), tx.Rollback())
		}
	}

	s.tx, s.stmts = tx, stmts
	return nil
}

func (s *Store) saveMessage(ctx context.Context, msg *eml.Message) (bool, error) {
	fromID, err := s.userID(ctx, msg.From, msg.FromName)
	if err != nil {
		return false, err
	}

	res, err := s.stmts.insertEmail.ExecContext(ctx, msg.MessageID, msg.Date.UnixMilli(), fromID, msg.Hash())
	if err != nil {
		return false, fmt.Errorf("insert email: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("email rows affected: %w", err)
	}
	return n > 0, nil
}

func (s *Store) userID(ctx context.Context, addr, name string) (int64, error) {
	if u, ok := s.users[addr]; ok && (u.named || name == "") {
		return u.id, nil
	}

	var id int64
	if err := s.stmts.upsertUser.QueryRowContext(ctx, addr, name).Scan(&id); err != nil {
		return 0, fmt.Errorf("upsert user %q: %w", addr, err)
	}
	s.users[addr] = cachedUser{id: id, named: name != ""}
	s.uncommitted = append(s.uncommitted, addr)
	return id, nil
}

// forgetUsers drops cache entries written since uncommitted had length mark.
// A dropped user is looked up from the database again when next seen.
func (s *Store) forgetUsers(mark int) {
	for _, addr := range s.uncommitted[mark:] {
		delete(s.users, addr)
	}
	s.uncommitted = s.uncommitted[:mark]
}

// Flush commits the current batch.
func (s *Store) Flush() error {
	if s.tx == nil {
		return nil
	}
	err := s.tx.Commit()
	s.tx, s.stmts = nil, txStmts{}
	s.pending = 0
	if err != nil {
		s.forgetUsers(0)
		return fmt.Errorf("commit: %w", err)
	}
	s.uncommitted = s.uncommitted[:0]
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

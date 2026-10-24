// Package eml parses the headers of Enron EML message files.
package eml

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strconv"
	"strings"
	"time"
)

// Message holds the header fields of an email that the processor cares about.
// Addresses are lower-cased and trimmed. The body is never read.
type Message struct {
	MessageID string
	Date      time.Time
	From      string
	// FromName is the display name of the sender from the X-From header, or
	// empty when it is missing.
	FromName string

	// Recipients are only used to compute Hash.
	to, cc, bcc []string
}

// Parse reads the headers of an EML message from r.
func Parse(r io.Reader) (*Message, error) {
	m, err := mail.ReadMessage(r)
	if err != nil {
		return nil, fmt.Errorf("read headers: %w", err)
	}
	h := m.Header

	msg := &Message{
		MessageID: strings.TrimSpace(h.Get("Message-ID")),
		FromName:  parseName(h.Get("X-From")),
		to:        splitAddresses(h.Get("To")),
		cc:        splitAddresses(h.Get("Cc")),
		bcc:       splitAddresses(h.Get("Bcc")),
	}
	if msg.MessageID == "" {
		return nil, errors.New("missing Message-ID header")
	}

	from := splitAddresses(h.Get("From"))
	if len(from) == 0 {
		return nil, errors.New("missing From header")
	}
	msg.From = from[0]

	rawDate := h.Get("Date")
	if rawDate == "" {
		return nil, errors.New("missing Date header")
	}
	msg.Date, err = mail.ParseDate(rawDate)
	if err != nil {
		return nil, fmt.Errorf("parse Date header: %w", err)
	}

	return msg, nil
}

// Hash identifies a message by its sender, recipients and date. Two files
// with the same hash are considered duplicates.
func (m *Message) Hash() string {
	key := strings.Join([]string{
		m.From,
		strings.Join(m.to, ","),
		strings.Join(m.cc, ","),
		strings.Join(m.bcc, ","),
		strconv.FormatInt(m.Date.UnixMilli(), 10),
	}, "|")
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func splitAddresses(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if addr := cleanAddress(part); addr != "" {
			out = append(out, addr)
		}
	}
	return out
}

// cleanAddress normalizes a single address. Some addresses in the dataset are
// mangled, such as "cater'.'john@enron.com" or "e-mail <'.'john@enron.com>":
// a display prefix and brackets are dropped and the quoted dot is unquoted.
// A quoted local part, as in `"fichter.john"@enron.com`, is unquoted so that
// it matches the plain form of the same address.
func cleanAddress(s string) string {
	s = strings.TrimSpace(s)
	if i, j := strings.Index(s, "<"), strings.LastIndex(s, ">"); i >= 0 && j > i {
		s = strings.TrimSpace(s[i+1 : j])
	}
	s = strings.ReplaceAll(s, "'.'", ".")
	s = strings.TrimPrefix(s, `"`)
	s = strings.Replace(s, `"@`, "@", 1)
	return strings.ToLower(strings.TrimSpace(s))
}

// parseName extracts a display name from the X-From header. Exchange style
// values carry a directory path, as in "Last, First </O=ENRON/...>", which is
// dropped along with surrounding quotes.
func parseName(s string) string {
	if i := strings.Index(s, "<"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(s), `"'`))
}

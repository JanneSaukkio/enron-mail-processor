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
	To        []string
	Cc        []string
	Bcc       []string

	// names maps an address to the display name found in the X-From, X-To,
	// X-cc or X-bcc headers.
	names map[string]string
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
		To:        splitAddresses(h.Get("To")),
		Cc:        splitAddresses(h.Get("Cc")),
		Bcc:       splitAddresses(h.Get("Bcc")),
		names:     make(map[string]string),
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

	msg.addNames(from, splitNames(h.Get("X-From")))
	msg.addNames(msg.To, splitNames(h.Get("X-To")))
	msg.addNames(msg.Cc, splitNames(h.Get("X-cc")))
	msg.addNames(msg.Bcc, splitNames(h.Get("X-bcc")))

	return msg, nil
}

// Name returns the display name of addr, or an empty string when the name
// could not be resolved.
func (m *Message) Name(addr string) string {
	return m.names[addr]
}

// Hash identifies a message by its sender, recipients and date. Two files
// with the same hash are considered duplicates.
func (m *Message) Hash() string {
	key := strings.Join([]string{
		m.From,
		strings.Join(m.To, ","),
		strings.Join(m.Cc, ","),
		strings.Join(m.Bcc, ","),
		strconv.FormatInt(m.Date.UnixMilli(), 10),
	}, "|")
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// addNames pairs addresses with names by position. The name headers are free
// text, so names are only trusted when both lists have the same length.
func (m *Message) addNames(addrs, names []string) {
	if len(addrs) != len(names) {
		return
	}
	for i, addr := range addrs {
		if _, ok := m.names[addr]; !ok && names[i] != "" {
			m.names[addr] = names[i]
		}
	}
}

func splitAddresses(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// splitNames splits a X-From/X-To/X-cc/X-bcc header into display names.
// Exchange style entries look like "Last, First </O=ENRON/...>", where the
// name itself contains a comma, so such lists are split on the closing
// bracket instead.
func splitNames(s string) []string {
	sep := ","
	if strings.Contains(s, "<") {
		sep = ">,"
	}
	var out []string
	for _, part := range strings.Split(s, sep) {
		if i := strings.Index(part, "<"); i >= 0 {
			part = part[:i]
		}
		part = strings.Trim(strings.TrimSpace(part), `"'`)
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

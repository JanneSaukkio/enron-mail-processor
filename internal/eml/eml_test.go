package eml

import (
	"net/mail"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func parseFile(t *testing.T, name string) *Message {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "test", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	msg, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return msg
}

func TestParseFiles(t *testing.T) {
	tests := []struct {
		file      string
		messageID string
		date      time.Time
		from      string
		fromName  string
		toLen     int
		firstTo   string
		lastTo    string
		ccLen     int
		bccLen    int
	}{
		{
			file:      "test-email.eml",
			messageID: "<31189653.1075855665329.JavaMail.evans@thyme>",
			date:      time.Date(2000, 12, 13, 18, 4, 0, 0, time.UTC),
			from:      "bounce-news-932653@lists.autoweb.com",
			fromName:  "bounce-news-932653@lists.autoweb.com",
			toLen:     1,
			firstTo:   "pallen@enron.com",
			lastTo:    "pallen@enron.com",
		},
		{
			file:      "test-email-cc.eml",
			messageID: "<30860170.1075846655972.JavaMail.evans@thyme>",
			date:      time.Date(2001, 6, 7, 9, 15, 0, 0, time.UTC),
			from:      "janette.elbertson@enron.com",
			fromName:  "Janette Elbertson",
			toLen:     98,
			firstTo:   "alan.aronowitz@enron.com",
			lastTo:    "claudia.meraz@enron.com",
			ccLen:     6,
			bccLen:    6,
		},
		{
			file:      "test-email-bcc.eml",
			messageID: "<16010313.1075853054745.JavaMail.evans@thyme>",
			date:      time.Date(2001, 10, 19, 19, 32, 36, 0, time.UTC),
			from:      "helpdesk.hottap@enron.com",
			fromName:  "Hottap HelpDesk",
			toLen:     5,
			firstTo:   "harry.woodson@enron.com",
			lastTo:    "bradley.holmes@enron.com",
			bccLen:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			msg := parseFile(t, tt.file)

			if msg.MessageID != tt.messageID {
				t.Errorf("MessageID = %q, want %q", msg.MessageID, tt.messageID)
			}
			if !msg.Date.Equal(tt.date) {
				t.Errorf("Date = %v, want %v", msg.Date, tt.date)
			}
			if msg.From != tt.from {
				t.Errorf("From = %q, want %q", msg.From, tt.from)
			}
			if msg.FromName != tt.fromName {
				t.Errorf("FromName = %q, want %q", msg.FromName, tt.fromName)
			}

			to, cc, bcc := recipients(t, tt.file)
			if len(to) != tt.toLen {
				t.Fatalf("len(to) = %d, want %d", len(to), tt.toLen)
			}
			if to[0] != tt.firstTo || to[len(to)-1] != tt.lastTo {
				t.Errorf("to = [%q ... %q], want [%q ... %q]", to[0], to[len(to)-1], tt.firstTo, tt.lastTo)
			}
			if len(cc) != tt.ccLen {
				t.Errorf("len(cc) = %d, want %d", len(cc), tt.ccLen)
			}
			if len(bcc) != tt.bccLen {
				t.Errorf("len(bcc) = %d, want %d", len(bcc), tt.bccLen)
			}
			if want := hash(msg.From, to, cc, bcc, msg.Date); msg.Hash != want {
				t.Errorf("Hash = %q, want %q", msg.Hash, want)
			}
		})
	}
}

// recipients splits the recipient headers of a test file the way Parse does
// before hashing them.
func recipients(t *testing.T, name string) (to, cc, bcc []string) {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "test", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := mail.ReadMessage(f)
	if err != nil {
		t.Fatal(err)
	}
	return splitAddresses(m.Header.Get("To")), splitAddresses(m.Header.Get("Cc")), splitAddresses(m.Header.Get("Bcc"))
}

func TestParseName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Janette Elbertson", "Janette Elbertson"},
		{"Hottap HelpDesk </O=ENRON/OU=NA/CN=RECIPIENTS/CN=MBX_HOTTAPHD>", "Hottap HelpDesk"},
		{"Allen, Phillip K. </O=ENRON/OU=NA/CN=RECIPIENTS/CN=Pallen>", "Allen, Phillip K."},
		{`"pallen@enron.com" <pallen@enron.com>`, "pallen@enron.com"},
		{"  ", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := parseName(tt.in); got != tt.want {
				t.Errorf("parseName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCleanAddress(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"john.doe@enron.com", "john.doe@enron.com"},
		{"  John.Doe@Enron.com ", "john.doe@enron.com"},
		{"cater'.'john@enron.com", "cater.john@enron.com"},
		{"e-mail <'.'john@enron.com>", ".john@enron.com"},
		{"<john.doe@enron.com>", "john.doe@enron.com"},
		{`<"fichter.john"@enron.com>`, "fichter.john@enron.com"},
		{`"fichter.john"@enron.com`, "fichter.john@enron.com"},
		{`"fichter.john@enron.com`, "fichter.john@enron.com"},
		{`fichter.john"@enron.com`, "fichter.john@enron.com"},
		{`fichter."john".x@enron.com`, `fichter."john".x@enron.com`},
		{`fichter.john@"enron".com`, `fichter.john@"enron".com`},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := cleanAddress(tt.in); got != tt.want {
				t.Errorf("cleanAddress(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseCleansAddresses(t *testing.T) {
	const input = "Message-ID: <1@x>\nDate: Mon, 14 May 2001 16:39:00 -0700 (PDT)\n" +
		"From: e-mail <'.'john@enron.com>\n" +
		"To: cater'.'john@enron.com, e-mail <'.'john@enron.com>\n" +
		"Cc: cater'.'john@enron.com\nBcc: e-mail <'.'john@enron.com>\n\nbody\n"

	msg, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}

	if want := ".john@enron.com"; msg.From != want {
		t.Errorf("From = %q, want %q", msg.From, want)
	}
	want := hash(".john@enron.com",
		[]string{"cater.john@enron.com", ".john@enron.com"},
		[]string{"cater.john@enron.com"},
		[]string{".john@enron.com"},
		msg.Date)
	if msg.Hash != want {
		t.Errorf("Hash = %q, want hash of the cleaned addresses %q", msg.Hash, want)
	}
}

func TestSplitAddresses(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"a@enron.com, b@enron.com", []string{"a@enron.com", "b@enron.com"}},
		{`"Doe, John" <john@enron.com>`, []string{"john@enron.com"}},
		{`"Doe, John" <john@enron.com>, "Roe, Jane" <jane@enron.com>`, []string{"john@enron.com", "jane@enron.com"}},
		{`"fichter.john"@enron.com, a@enron.com`, []string{"fichter.john@enron.com", "a@enron.com"}},
		{`"fichter.john@enron.com, a@enron.com`, []string{"fichter.john@enron.com", "a@enron.com"}},
		{" , ,", nil},
		{"", nil},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := splitAddresses(tt.in); !slices.Equal(got, tt.want) {
				t.Errorf("splitAddresses(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseQuotedFromName(t *testing.T) {
	const input = "Message-ID: <1@x>\nDate: Mon, 14 May 2001 16:39:00 -0700 (PDT)\n" +
		"From: \"Doe, John\" <John.Doe@enron.com>\n\nbody\n"

	msg, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if want := "john.doe@enron.com"; msg.From != want {
		t.Errorf("From = %q, want %q", msg.From, want)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"missing message id", "Date: Mon, 14 May 2001 16:39:00 -0700 (PDT)\nFrom: a@enron.com\n\nbody\n"},
		{"missing from", "Message-ID: <1@x>\nDate: Mon, 14 May 2001 16:39:00 -0700 (PDT)\n\nbody\n"},
		{"missing date", "Message-ID: <1@x>\nFrom: a@enron.com\n\nbody\n"},
		{"invalid date", "Message-ID: <1@x>\nDate: yesterday\nFrom: a@enron.com\n\nbody\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse(strings.NewReader(tt.input)); err == nil {
				t.Error("Parse() error = nil, want error")
			}
		})
	}
}

func TestHash(t *testing.T) {
	const base = "Message-ID: <1@x>\nDate: Mon, 14 May 2001 16:39:00 -0700 (PDT)\nFrom: a@enron.com\nTo: b@enron.com\n"

	parse := func(s string) *Message {
		t.Helper()
		msg, err := Parse(strings.NewReader(s + "\nbody\n"))
		if err != nil {
			t.Fatal(err)
		}
		return msg
	}

	h := parse(base).Hash
	if len(h) != 64 {
		t.Fatalf("Hash length = %d, want 64", len(h))
	}

	// Message-ID and display names are not part of the hash.
	same := parse(strings.Replace(base, "<1@x>", "<2@x>", 1) + "X-From: A\n")
	if same.Hash != h {
		t.Error("Hash differs for messages with same sender, recipients and date")
	}

	different := []string{
		strings.Replace(base, "16:39:00", "16:39:01", 1),
		strings.Replace(base, "b@enron.com", "c@enron.com", 1),
		base + "Cc: c@enron.com\n",
		base + "Bcc: c@enron.com\n",
	}
	for _, s := range different {
		if parse(s).Hash == h {
			t.Errorf("Hash equal for different message:\n%s", s)
		}
	}
}

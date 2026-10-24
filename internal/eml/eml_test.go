package eml

import (
	"os"
	"path/filepath"
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
		names     map[string]string
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
			names:     map[string]string{"pallen@enron.com": "pallen@enron.com"},
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
			names: map[string]string{
				"alan.aronowitz@enron.com":  "Alan Aronowitz",
				"sandi.braband@enron.com":   "Sandi M Braband",
				"gary.bode@enron.com":       "Gary Bode",
				"stephanie.truss@enron.com": "Stephanie Truss",
			},
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
			names: map[string]string{
				"harry.woodson@enron.com":  "Woodson, Harry",
				"bradley.holmes@enron.com": "Holmes, Bradley",
				"debra.scurlock@enron.com": "Scurlock, Debra",
			},
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
			if got := msg.Name(msg.From); got != tt.fromName {
				t.Errorf("Name(From) = %q, want %q", got, tt.fromName)
			}
			if len(msg.To) != tt.toLen {
				t.Fatalf("len(To) = %d, want %d", len(msg.To), tt.toLen)
			}
			if msg.To[0] != tt.firstTo || msg.To[len(msg.To)-1] != tt.lastTo {
				t.Errorf("To = [%q ... %q], want [%q ... %q]", msg.To[0], msg.To[len(msg.To)-1], tt.firstTo, tt.lastTo)
			}
			if len(msg.Cc) != tt.ccLen {
				t.Errorf("len(Cc) = %d, want %d", len(msg.Cc), tt.ccLen)
			}
			if len(msg.Bcc) != tt.bccLen {
				t.Errorf("len(Bcc) = %d, want %d", len(msg.Bcc), tt.bccLen)
			}
			for addr, want := range tt.names {
				if got := msg.Name(addr); got != want {
					t.Errorf("Name(%q) = %q, want %q", addr, got, want)
				}
			}
		})
	}
}

func TestParseNamesWithMismatchedCount(t *testing.T) {
	const input = "Message-ID: <1@x>\nDate: Mon, 14 May 2001 16:39:00 -0700 (PDT)\n" +
		"From: a@enron.com\nTo: b@enron.com, c@enron.com\nX-From: A\nX-To: B\n\nbody\n"
	msg, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if got := msg.Name("a@enron.com"); got != "A" {
		t.Errorf("Name(a) = %q, want %q", got, "A")
	}
	// Two addresses but one name: positions can't be trusted.
	for _, addr := range msg.To {
		if got := msg.Name(addr); got != "" {
			t.Errorf("Name(%q) = %q, want empty", addr, got)
		}
	}
	if got := msg.Name("nobody@enron.com"); got != "" {
		t.Errorf("Name(unknown) = %q, want empty", got)
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

	h := parse(base).Hash()
	if len(h) != 64 {
		t.Fatalf("Hash() length = %d, want 64", len(h))
	}

	// Message-ID and display names are not part of the hash.
	same := parse(strings.Replace(base, "<1@x>", "<2@x>", 1) + "X-From: A\n")
	if same.Hash() != h {
		t.Error("Hash() differs for messages with same sender, recipients and date")
	}

	different := []string{
		strings.Replace(base, "16:39:00", "16:39:01", 1),
		strings.Replace(base, "b@enron.com", "c@enron.com", 1),
		base + "Cc: c@enron.com\n",
		base + "Bcc: c@enron.com\n",
	}
	for _, s := range different {
		if parse(s).Hash() == h {
			t.Errorf("Hash() equal for different message:\n%s", s)
		}
	}
}

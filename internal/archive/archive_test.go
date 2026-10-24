package archive

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"testing"
)

var testArchive = filepath.Join("..", "..", "test", "test-dataset.tar.gz")

func TestWalkReadsAllFiles(t *testing.T) {
	want := []string{
		"maildir/user1/1.",
		"maildir/user1/sent/1.",
		"maildir/user1/sent/2.",
		"maildir/user2/_sent_mail/1.",
	}

	var got []string
	err := Walk(context.Background(), testArchive, func(name string, r io.Reader) error {
		b, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		if len(b) == 0 {
			t.Errorf("%s: empty content", name)
		}
		got = append(got, name)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk() error = %v", err)
	}

	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("Walk() visited %v, want %v", got, want)
	}
}

func TestWalkErrors(t *testing.T) {
	noop := func(string, io.Reader) error { return nil }
	errStop := errors.New("stop")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name    string
		ctx     context.Context
		path    string
		fn      WalkFunc
		wantErr error
	}{
		{"missing file", context.Background(), "does-not-exist.tar.gz", noop, nil},
		{"not gzip", context.Background(), "archive.go", noop, nil},
		{"callback error", context.Background(), testArchive, func(string, io.Reader) error { return errStop }, errStop},
		{"cancelled context", cancelled, testArchive, noop, context.Canceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Walk(tt.ctx, tt.path, tt.fn)
			if err == nil {
				t.Fatal("Walk() error = nil, want error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("Walk() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

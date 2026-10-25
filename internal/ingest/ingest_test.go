package ingest

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/JanneSaukkio/enron-mail-processor/internal/eml"
	"github.com/JanneSaukkio/enron-mail-processor/internal/store"
)

var testArchive = filepath.Join("..", "..", "test", "test-dataset.tar.gz")

// fakeStore inserts every message into a batch and loses the batch when a
// commit fails, like store.Store.
type fakeStore struct {
	failSave  int // 1-based SaveMessage call whose batch commit fails; 0 never
	failFlush bool

	calls, batch, stored int
}

var errCommit = errors.New("commit failed")

func (f *fakeStore) SaveMessage(context.Context, *eml.Message) (bool, error) {
	f.calls++
	if f.calls == f.failSave {
		lost := f.batch
		f.batch = 0
		return false, &store.CommitError{Lost: lost, Err: errCommit}
	}
	f.batch++
	return true, nil
}

func (f *fakeStore) Flush() error {
	if f.failFlush {
		lost := f.batch
		f.batch = 0
		return &store.CommitError{Lost: lost, Err: errCommit}
	}
	f.stored += f.batch
	f.batch = 0
	return nil
}

func (f *fakeStore) Stats(context.Context) (store.Stats, error) {
	return store.Stats{Messages: int64(f.stored)}, nil
}

func TestIngestCountsLostMessagesAsErrors(t *testing.T) {
	tests := []struct {
		name string
		st   *fakeStore
	}{
		{"no failure", &fakeStore{}},
		{"commit fails in SaveMessage", &fakeStore{failSave: 3}},
		{"commit fails in Flush", &fakeStore{failFlush: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := New(tt.st, Config{ArchivePath: testArchive, Workers: 2}, slog.New(slog.DiscardHandler))
			t.Cleanup(svc.Close)
			svc.Start()
			svc.wg.Wait()

			processed, errs, dups := svc.processed.Load(), svc.errors.Load(), svc.duplicates.Load()
			if processed != 4 {
				t.Fatalf("processed = %d, want 4", processed)
			}
			if got := int64(tt.st.stored) + errs + dups; got != processed {
				t.Errorf("stored %d + errors %d + duplicates %d = %d, want processed %d",
					tt.st.stored, errs, dups, got, processed)
			}
		})
	}
}

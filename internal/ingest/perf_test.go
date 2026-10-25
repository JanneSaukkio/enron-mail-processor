//go:build perf

package ingest

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JanneSaukkio/enron-mail-processor/internal/archive"
	"github.com/JanneSaukkio/enron-mail-processor/internal/store"
)

var perfArchive = filepath.Join("..", "..", "test", "perf-test-dataset.tar.gz")

// TestIngestPerformance ingests the performance dataset into a temporary
// SQLite database and reports the throughput. It needs a database, so it is
// only built with the perf tag. -count=1 bypasses the test cache:
//
//	go test -tags perf -run TestIngestPerformance -v -count=1 -timeout 10m ./internal/ingest/
func TestIngestPerformance(t *testing.T) {
	if _, err := os.Stat(perfArchive); err != nil {
		t.Skipf("performance dataset not available: %v", err)
	}

	files := countFiles(t)

	for _, tt := range []struct {
		name   string
		memory bool
	}{
		{"disk", false},
		{"memory", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ingestPerf(t, files, tt.memory)
		})
	}
}

func ingestPerf(t *testing.T, files int64, memory bool) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "perf.db")
	if memory {
		dbPath = store.Memory
	}
	st, err := store.Open(ctx, dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})

	resultsPath := filepath.Join(dir, "results.txt")
	svc := New(st, Config{ArchivePath: perfArchive, ResultsPath: resultsPath}, slog.New(slog.DiscardHandler))
	t.Cleanup(svc.Close)

	start := time.Now()
	svc.Start()
	svc.wg.Wait()
	took := time.Since(start)

	status := svc.Status()
	if !status.Finished {
		t.Error("Status().Finished = false after run")
	}
	if status.ProcessedCount != files {
		t.Errorf("ProcessedCount = %d, want %d", status.ProcessedCount, files)
	}

	stats, err := st.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	errs, dups := svc.errors.Load(), svc.duplicates.Load()
	if got := stats.Messages + errs + dups; got != files {
		t.Errorf("stored %d + errors %d + duplicates %d = %d, want %d files", stats.Messages, errs, dups, got, files)
	}

	results, err := os.ReadFile(resultsPath)
	if err != nil {
		t.Fatalf("read results: %v", err)
	}

	t.Logf("ingested %d files in %v (%.0f files/s)", files, took.Round(time.Millisecond), float64(files)/took.Seconds())
	t.Logf("results.txt:\n%s", results)
}

func countFiles(t *testing.T) int64 {
	t.Helper()
	var n int64
	err := archive.Walk(context.Background(), perfArchive, func(string, io.Reader) error {
		n++
		return nil
	})
	if err != nil {
		t.Fatalf("count files: %v", err)
	}
	return n
}

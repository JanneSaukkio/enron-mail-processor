// Package ingest runs the ingestion of an email archive into a store.
package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JanneSaukkio/enron-mail-processor/internal/archive"
	"github.com/JanneSaukkio/enron-mail-processor/internal/eml"
	"github.com/JanneSaukkio/enron-mail-processor/internal/store"
)

// Store persists parsed messages.
type Store interface {
	SaveMessage(ctx context.Context, msg *eml.Message) (inserted bool, err error)
	Flush() error
	Stats(ctx context.Context) (store.Stats, error)
}

// Status describes the latest ingestion run.
type Status struct {
	Finished       bool  `json:"finished"`
	ProcessedCount int64 `json:"processed_count"`
}

// Config configures a Service.
type Config struct {
	// ArchivePath is the gzip compressed TAR archive to ingest.
	ArchivePath string
	// ResultsPath is where results are written after each run. Empty
	// disables writing results.
	ResultsPath string
	// Workers is the number of goroutines parsing messages. Zero uses
	// GOMAXPROCS.
	Workers int
}

// Service runs at most one ingestion at a time in the background.
type Service struct {
	store  Store
	cfg    Config
	logger *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu         sync.Mutex
	running    bool
	startedAt  time.Time
	finishedAt time.Time

	processed  atomic.Int64
	errors     atomic.Int64
	duplicates atomic.Int64
}

// New returns a Service. Call Close to stop a running ingestion.
func New(st Store, cfg Config, logger *slog.Logger) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{store: st, cfg: cfg, logger: logger, ctx: ctx, cancel: cancel}
}

// Start begins a new ingestion unless one is already running, and returns
// the start time of the current run.
func (s *Service) Start() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return s.startedAt
	}

	s.running = true
	s.startedAt = time.Now().UTC()
	s.finishedAt = time.Time{}
	s.processed.Store(0)
	s.errors.Store(0)
	s.duplicates.Store(0)

	s.wg.Add(1)
	go func(startedAt time.Time) {
		defer s.wg.Done()
		s.run(startedAt)
	}(s.startedAt)

	return s.startedAt
}

// Status returns the state of the latest run.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{
		Finished:       !s.running && !s.startedAt.IsZero(),
		ProcessedCount: s.processed.Load(),
	}
}

// Close stops a running ingestion and waits for it to finish.
func (s *Service) Close() {
	s.cancel()
	s.wg.Wait()
}

func (s *Service) run(startedAt time.Time) {
	s.logger.Info("ingestion started", "archive", s.cfg.ArchivePath)

	// Finishing the run and writing results must happen even when the
	// ingestion fails or panics.
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("ingestion panicked", "panic", r)
		}
		s.finish(startedAt)
	}()

	err := s.ingest()
	if flushErr := s.store.Flush(); flushErr != nil {
		s.errors.Add(lost(flushErr))
		if err == nil {
			err = flushErr
		}
	}
	if err != nil {
		s.logger.Error("ingestion failed", "error", err)
	}
}

type file struct {
	name string
	data []byte
}

type parsed struct {
	name string
	msg  *eml.Message
	err  error
}

// ingest runs a pipeline of one archive reader, a pool of parsers and one
// writer. Reading the gzip stream is sequential and SQLite allows a single
// writer only, so parsing is the stage that runs in parallel; the reader and
// the writer overlap with it and each other.
func (s *Service) ingest() error {
	workers := s.cfg.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}

	files := make(chan file, workers*2)
	results := make(chan parsed, workers*2)

	var walkErr error
	go func() {
		defer close(files)
		walkErr = archive.Walk(s.ctx, s.cfg.ArchivePath, func(name string, r io.Reader) error {
			data, err := io.ReadAll(r)
			if err != nil {
				return err
			}
			select {
			case files <- file{name, data}:
				return nil
			case <-s.ctx.Done():
				return s.ctx.Err()
			}
		})
	}()

	var parsers sync.WaitGroup
	for range workers {
		parsers.Go(func() {
			for f := range files {
				msg, err := eml.Parse(bytes.NewReader(f.data))
				results <- parsed{f.name, msg, err}
			}
		})
	}
	go func() {
		parsers.Wait()
		close(results)
	}()

	// The writer drains results until the parsers are done, so that the
	// pipeline never blocks, even when the context is cancelled.
	for r := range results {
		s.save(r)
	}

	// results is closed only after files is closed, so walkErr is set.
	return walkErr
}

// save stores one parsed file. Errors in a single file are counted and never
// stop the ingestion.
func (s *Service) save(r parsed) {
	defer s.processed.Add(1)

	if r.err != nil {
		s.errors.Add(1)
		s.logger.Warn("parse message", "file", r.name, "error", r.err)
		return
	}

	inserted, err := s.store.SaveMessage(s.ctx, r.msg)
	switch {
	case err != nil:
		s.errors.Add(1 + lost(err))
		s.logger.Warn("save message", "file", r.name, "error", err)
	case !inserted:
		s.duplicates.Add(1)
	}
}

// lost returns the number of messages that were counted as stored but were
// lost with a failed batch commit. They are counted as errors instead.
func lost(err error) int64 {
	var ce *store.CommitError
	if errors.As(err, &ce) {
		return int64(ce.Lost)
	}
	return 0
}

func (s *Service) finish(startedAt time.Time) {
	finishedAt := time.Now().UTC()
	s.mu.Lock()
	s.finishedAt = finishedAt
	s.mu.Unlock()

	if s.cfg.ResultsPath != "" {
		if err := s.writeResults(startedAt); err != nil {
			s.logger.Error("write results", "error", err)
		}
	}

	// The run is marked done only after results are written, so that a new
	// run cannot reset the counters while they are being printed.
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()

	s.logger.Info("ingestion finished",
		"processed", s.processed.Load(),
		"errors", s.errors.Load(),
		"duplicates", s.duplicates.Load(),
		"took", finishedAt.Sub(startedAt))
}

func (s *Service) writeResults(startedAt time.Time) error {
	stats, err := s.store.Stats(context.WithoutCancel(s.ctx))
	if err != nil {
		return err
	}

	content := fmt.Sprintf(
		"message_count: %d\nsender_count: %d\nmessages_processed: %d\n"+
			"processing_errors: %d\nduplicates_ignored: %d\ntime_took: %d\n",
		stats.Messages, stats.Senders, s.processed.Load(),
		s.errors.Load(), s.duplicates.Load(), time.Since(startedAt).Milliseconds())

	return os.WriteFile(s.cfg.ResultsPath, []byte(content), 0o644)
}

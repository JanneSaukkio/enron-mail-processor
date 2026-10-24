// Command processor ingests the Enron email archive into SQLite and serves
// sender statistics over HTTP.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/JanneSaukkio/enron-mail-processor/internal/api"
	"github.com/JanneSaukkio/enron-mail-processor/internal/ingest"
	"github.com/JanneSaukkio/enron-mail-processor/internal/store"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	path := flag.String("path", "./enron_mail_20150507.tar.gz", "path to the archive file")
	clean := flag.Bool("clean", true, "reinitialize the database schema")
	results := flag.Bool("results", true, "write results.txt after processing stops")
	dbPath := flag.String("db", "./enron.db", `path to the SQLite database, or "`+store.Memory+`" for an in-memory database`)
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	if fi, err := os.Stat(*path); err != nil {
		return fmt.Errorf("archive: %w", err)
	} else if !fi.Mode().IsRegular() {
		return fmt.Errorf("archive %s is not a regular file", *path)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, *dbPath, *clean)
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.Error("close database", "error", err)
		}
	}()

	cfg := ingest.Config{ArchivePath: *path}
	if *results {
		cfg.ResultsPath = "results.txt"
	}
	svc := ingest.New(st, cfg, logger)
	defer svc.Close()

	srv := &http.Server{
		Addr:              *addr,
		Handler:           api.New(svc, st, logger),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", *addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

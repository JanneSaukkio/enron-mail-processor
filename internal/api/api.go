// Package api exposes the ingestion over HTTP.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/JanneSaukkio/enron-mail-processor/internal/ingest"
	"github.com/JanneSaukkio/enron-mail-processor/internal/store"
)

// Ingester starts and reports on ingestion runs.
type Ingester interface {
	Start() time.Time
	Status() ingest.Status
}

// SenderLister lists senders by the number of messages sent.
type SenderLister interface {
	TopSenders(ctx context.Context, limit, offset int) ([]store.Sender, int64, error)
}

// Handler serves the HTTP API.
type Handler struct {
	ingester Ingester
	senders  SenderLister
	logger   *slog.Logger
}

// New returns an http.Handler with all API routes registered.
func New(ingester Ingester, senders SenderLister, logger *slog.Logger) http.Handler {
	h := &Handler{ingester: ingester, senders: senders, logger: logger}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /start", h.start)
	mux.HandleFunc("GET /status", h.status)
	mux.HandleFunc("GET /top-senders", h.topSenders)
	return mux
}

func (h *Handler) start(w http.ResponseWriter, _ *http.Request) {
	h.writeJSON(w, http.StatusOK, struct {
		StartedAt time.Time `json:"started_at"`
	}{h.ingester.Start()})
}

func (h *Handler) status(w http.ResponseWriter, _ *http.Request) {
	h.writeJSON(w, http.StatusOK, h.ingester.Status())
}

func (h *Handler) topSenders(w http.ResponseWriter, r *http.Request) {
	limit, ok := intParam(r, "limit", 10)
	if !ok || limit < 1 {
		http.Error(w, "limit must be a positive integer", http.StatusBadRequest)
		return
	}
	offset, ok := intParam(r, "offset", 0)
	if !ok || offset < 0 {
		http.Error(w, "offset must be a non-negative integer", http.StatusBadRequest)
		return
	}

	senders, total, err := h.senders.TopSenders(r.Context(), limit, offset)
	if err != nil {
		h.logger.Error("top senders", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	h.writeJSON(w, http.StatusOK, struct {
		Total   int64          `json:"total"`
		Objects []store.Sender `json:"objects"`
	}{total, senders})
}

func intParam(r *http.Request, name string, def int) (int, bool) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def, true
	}
	v, err := strconv.Atoi(s)
	return v, err == nil
}

func (h *Handler) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		h.logger.Warn("write response", "error", err)
	}
}

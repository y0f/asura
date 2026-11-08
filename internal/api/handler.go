package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/monitor"
	"github.com/y0f/asura/internal/notifier"
	"github.com/y0f/asura/internal/storage"
)

type Handler struct {
	cfg                *config.Config
	store              storage.Store
	pipeline           *monitor.Pipeline
	notifier           *notifier.Dispatcher
	logger             *slog.Logger
	startTime          time.Time
	OnStatusPageChange func()
	// SubNotifier sends status page subscriber emails; nil when
	// subscriptions are disabled.
	SubNotifier *notifier.SubscriberNotifier
	// loginRL throttles status page password attempts per client IP. It is
	// the web UI's login limiter, so the API and the web password forms share
	// one budget of attempts.
	loginRL *httputil.RateLimiter
}

// New returns the API handler. loginRL is the limiter shared with the web
// UI's login and status page password forms; the caller owns and stops it.
func New(cfg *config.Config, store storage.Store, pipeline *monitor.Pipeline,
	dispatcher *notifier.Dispatcher, logger *slog.Logger, loginRL *httputil.RateLimiter) *Handler {
	return &Handler{
		cfg:       cfg,
		store:     store,
		pipeline:  pipeline,
		notifier:  dispatcher,
		logger:    logger,
		startTime: time.Now(),
		loginRL:   loginRL,
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// readJSON accepts exactly one JSON document. Decoder errors may include
// submitted field names or values, so neither responses nor logs repeat them.
func (h *Handler) readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("request body is empty")
		}
		h.logger.Warn("invalid JSON request body", "method", r.Method, "route", r.Pattern)
		return fmt.Errorf("invalid request body")
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		h.logger.Warn("invalid JSON request body", "method", r.Method, "route", r.Pattern)
		return fmt.Errorf("request body must contain exactly one JSON document")
	}
	return nil
}

func (h *Handler) audit(r *http.Request, action, entity string, entityID int64, detail string) {
	entry := &storage.AuditEntry{
		Action:     action,
		Entity:     entity,
		EntityID:   entityID,
		APIKeyName: httputil.GetAPIKeyName(r.Context()),
		Detail:     detail,
	}
	if err := h.store.InsertAudit(r.Context(), entry); err != nil {
		h.logger.Error("audit log failed", "error", err)
	}
}

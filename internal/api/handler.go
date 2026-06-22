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
}

func New(cfg *config.Config, store storage.Store, pipeline *monitor.Pipeline,
	dispatcher *notifier.Dispatcher, logger *slog.Logger) *Handler {
	return &Handler{
		cfg:       cfg,
		store:     store,
		pipeline:  pipeline,
		notifier:  dispatcher,
		logger:    logger,
		startTime: time.Now(),
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

// readJSON decodes the request body into v. Detailed decoder errors are logged
// server-side; callers receive a generic message so internal struct details and
// parser internals are not leaked to clients.
func (h *Handler) readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("request body is empty")
		}
		h.logger.Warn("decode request body", "method", r.Method, "path", r.URL.Path, "error", err)
		return fmt.Errorf("invalid request body")
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

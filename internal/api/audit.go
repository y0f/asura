package api

import (
	"net/http"
	"time"

	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/storage"
)

func (h *Handler) ListAuditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := httputil.ParsePagination(r)

	f := storage.AuditLogFilter{
		Action:     q.Get("action"),
		Entity:     q.Get("entity"),
		APIKeyName: q.Get("api_key"),
	}
	if from := q.Get("from"); from != "" {
		if t, err := time.Parse(time.RFC3339, from); err == nil {
			f.From = t
		}
	}
	if to := q.Get("to"); to != "" {
		if t, err := time.Parse(time.RFC3339, to); err == nil {
			f.To = t
		}
	}

	result, err := h.store.ListAuditLog(r.Context(), f, p)
	if err != nil {
		h.logger.Error("api: list audit log", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list audit log")
		return
	}

	writeJSON(w, http.StatusOK, result)
}

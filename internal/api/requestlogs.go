package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
)

func (h *Handler) ListRequestLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := httputil.ParsePagination(r)

	f := storage.RequestLogFilter{
		Access:     httputil.MonitorAccess(r.Context(), "request_logs.read"),
		RouteGroup: q.Get("group"),
		Method:     q.Get("method"),
	}

	if sc := q.Get("status_code"); sc != "" {
		if code, err := strconv.Atoi(sc); err == nil {
			f.StatusCode = code
		}
	}
	if mid := q.Get("monitor_id"); mid != "" {
		if id, err := strconv.ParseInt(mid, 10, 64); err == nil {
			f.MonitorID = &id
		}
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

	result, err := h.store.ListRequestLogs(r.Context(), f, p)
	if err != nil {
		h.logger.Error("api: list request logs", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list request logs")
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) RequestLogStats(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	to := now

	if f := r.URL.Query().Get("from"); f != "" {
		if t, err := time.Parse(time.RFC3339, f); err == nil {
			from = t
		}
	}
	if t := r.URL.Query().Get("to"); t != "" {
		if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			to = parsed
		}
	}

	stats, err := h.store.GetRequestLogStats(r.Context(), from, to, httputil.MonitorAccess(r.Context(), "request_logs.read")...)
	if err != nil {
		h.logger.Error("api: request log stats", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get request log stats")
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

package web

import (
	"net/http"
	"time"

	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/storage"
	"github.com/y0f/asura/v3/internal/web/views"
)

func (h *Handler) AuditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := httputil.ParsePagination(r)

	f := storage.AuditLogFilter{
		Action:     q.Get("action"),
		Entity:     q.Get("entity"),
		APIKeyName: q.Get("api_key"),
	}

	now := time.Now().UTC()
	timeRange := q.Get("range")
	switch timeRange {
	case "24h":
		f.From = now.Add(-24 * time.Hour)
	case "30d":
		f.From = now.AddDate(0, 0, -30)
	default:
		timeRange = "7d"
		f.From = now.AddDate(0, 0, -7)
	}
	f.To = now

	result, err := h.store.ListAuditLog(r.Context(), f, p)
	if err != nil {
		h.logger.Error("web: list audit log", "error", err)
	}

	lp := h.newLayoutParams(r, "Audit Log", "audit")
	h.renderComponent(w, r, views.AuditLogPage(views.AuditLogParams{
		LayoutParams: lp,
		Result:       result,
		Filter:       f,
		TimeRange:    timeRange,
	}))
}

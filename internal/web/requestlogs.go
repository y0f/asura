package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/y0f/asura/v2/internal/httputil"
	"github.com/y0f/asura/v2/internal/storage"
	"github.com/y0f/asura/v2/internal/web/views"
)

func (h *Handler) RequestLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := httputil.ParsePagination(r)

	f := storage.RequestLogFilter{
		Access:     httputil.MonitorAccess(r.Context(), "request_logs.read"),
		RouteGroup: q.Get("group"),
		Method:     q.Get("method"),
		ClientIP:   q.Get("ip"),
	}

	if sc := q.Get("status_code"); sc != "" {
		if code, err := strconv.Atoi(sc); err == nil {
			f.StatusCode = code
		}
	}

	now := time.Now().UTC()
	switch q.Get("range") {
	case "1h":
		f.From = now.Add(-1 * time.Hour)
	case "12h":
		f.From = now.Add(-12 * time.Hour)
	case "7d":
		f.From = now.AddDate(0, 0, -7)
	default:
		f.From = now.Add(-24 * time.Hour)
	}
	f.To = now

	result, err := h.store.ListRequestLogs(r.Context(), f, p)
	if err != nil {
		h.logger.Error("web: list request logs", "error", err)
		http.Error(w, "Failed to load request logs", http.StatusInternalServerError)
		return
	}

	stats, err := h.store.GetRequestLogStats(r.Context(), f.From, f.To, f.Access...)
	if err != nil {
		h.logger.Error("web: get request log stats", "error", err)
		http.Error(w, "Failed to load request logs", http.StatusInternalServerError)
		return
	}

	topIPs, err := h.store.ListTopClientIPs(r.Context(), f.From, f.To, 50, f.Access...)
	if err != nil {
		h.logger.Error("web: list top client IPs", "error", err)
		http.Error(w, "Failed to load request logs", http.StatusInternalServerError)
		return
	}
	if f.ClientIP != "" {
		found := false
		for _, ip := range topIPs {
			if ip == f.ClientIP {
				found = true
				break
			}
		}
		if !found {
			topIPs = append([]string{f.ClientIP}, topIPs...)
		}
	}

	timeRange := q.Get("range")
	if timeRange == "" {
		timeRange = "24h"
	}

	lp := h.newLayoutParams(r, "Request Logs", "logs")
	h.renderComponent(w, r, views.RequestLogListPage(views.RequestLogParams{
		LayoutParams: lp,
		Result:       result,
		Stats:        stats,
		Filter:       f.RouteGroup,
		ClientIP:     f.ClientIP,
		TimeRange:    timeRange,
		TopIPs:       topIPs,
	}))
}

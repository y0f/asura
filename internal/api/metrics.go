package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/incident"
	"github.com/y0f/asura/internal/storage"
)

func (h *Handler) Metrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var sb strings.Builder

	up, down, degraded, paused, pending, err := h.store.CountMonitorsByStatus(ctx)
	if err != nil {
		h.logger.Error("metrics: count monitors", "error", err)
		writeError(w, http.StatusInternalServerError, "metrics error")
		return
	}

	sb.WriteString("# HELP asura_monitors_total Total number of monitors by status.\n")
	sb.WriteString("# TYPE asura_monitors_total gauge\n")
	fmt.Fprintf(&sb, "asura_monitors_total{status=\"up\"} %d\n", up)
	fmt.Fprintf(&sb, "asura_monitors_total{status=\"down\"} %d\n", down)
	fmt.Fprintf(&sb, "asura_monitors_total{status=\"degraded\"} %d\n", degraded)
	fmt.Fprintf(&sb, "asura_monitors_total{status=\"paused\"} %d\n", paused)
	fmt.Fprintf(&sb, "asura_monitors_total{status=\"pending\"} %d\n", pending)

	monitors, err := h.store.ListMonitors(ctx, storage.MonitorListFilter{}, storage.Pagination{Page: 1, PerPage: 1000})
	if err != nil {
		h.logger.Error("metrics: list monitors", "error", err)
		writeError(w, http.StatusInternalServerError, "metrics error")
		return
	}

	monList, ok := monitors.Data.([]*storage.Monitor)
	if ok && len(monList) > 0 {
		h.writeMonitorMetrics(&sb, ctx, monList)
	}

	h.writeIncidentMetrics(&sb, ctx)
	h.writeRequestMetrics(&sb, ctx)

	if h.pipeline != nil {
		sb.WriteString("\n# HELP asura_scheduler_jobs_dropped_total Total scheduler jobs dropped due to full channel.\n")
		sb.WriteString("# TYPE asura_scheduler_jobs_dropped_total counter\n")
		fmt.Fprintf(&sb, "asura_scheduler_jobs_dropped_total %d\n", h.pipeline.DroppedJobs())

		sb.WriteString("\n# HELP asura_notifications_dropped_total Total notification events dropped due to full channel.\n")
		sb.WriteString("# TYPE asura_notifications_dropped_total counter\n")
		fmt.Fprintf(&sb, "asura_notifications_dropped_total %d\n", h.pipeline.DroppedNotifications())
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	io.WriteString(w, sb.String())
}

func (h *Handler) writeMonitorMetrics(sb *strings.Builder, ctx context.Context, monList []*storage.Monitor) {
	monIDs := make([]int64, len(monList))
	for i, m := range monList {
		monIDs[i] = m.ID
	}
	tagMap, _ := h.store.GetMonitorTagsBatch(ctx, monIDs)

	sb.WriteString("\n# HELP asura_monitor_up Whether the monitor is up (1) or down (0).\n")
	sb.WriteString("# TYPE asura_monitor_up gauge\n")
	for _, m := range monList {
		val := 0
		if m.Status == "up" {
			val = 1
		}
		tagsLabel := formatTagsLabel(tagMap[m.ID])
		fmt.Fprintf(sb, "asura_monitor_up{id=\"%d\",name=\"%s\",type=\"%s\"%s} %d\n",
			m.ID, escapeProm(m.Name), m.Type, tagsLabel, val)
	}

	rtMap, err := h.store.GetLatestResponseTimes(ctx)
	if err == nil && len(rtMap) > 0 {
		sb.WriteString("\n# HELP asura_monitor_response_time_ms Last response time in milliseconds.\n")
		sb.WriteString("# TYPE asura_monitor_response_time_ms gauge\n")
		for _, m := range monList {
			if rt, ok := rtMap[m.ID]; ok && m.HasSampledMetrics() {
				fmt.Fprintf(sb, "asura_monitor_response_time_ms{id=\"%d\",name=\"%s\"} %d\n",
					m.ID, escapeProm(m.Name), rt)
			}
		}
	}

	sb.WriteString("\n# HELP asura_monitor_consecutive_failures Current consecutive failure count.\n")
	sb.WriteString("# TYPE asura_monitor_consecutive_failures gauge\n")
	for _, m := range monList {
		fmt.Fprintf(sb, "asura_monitor_consecutive_failures{id=\"%d\",name=\"%s\"} %d\n",
			m.ID, escapeProm(m.Name), m.ConsecFails)
	}

	sb.WriteString("\n# HELP asura_monitor_consecutive_successes Current consecutive success count.\n")
	sb.WriteString("# TYPE asura_monitor_consecutive_successes gauge\n")
	for _, m := range monList {
		fmt.Fprintf(sb, "asura_monitor_consecutive_successes{id=\"%d\",name=\"%s\"} %d\n",
			m.ID, escapeProm(m.Name), m.ConsecSuccesses)
	}

	typeCounts := make(map[string]int)
	for _, m := range monList {
		typeCounts[m.Type]++
	}
	sb.WriteString("\n# HELP asura_monitors_by_type Total monitors by type.\n")
	sb.WriteString("# TYPE asura_monitors_by_type gauge\n")
	for t, c := range typeCounts {
		fmt.Fprintf(sb, "asura_monitors_by_type{type=\"%s\"} %d\n", t, c)
	}
}

func (h *Handler) writeIncidentMetrics(sb *strings.Builder, ctx context.Context) {
	sb.WriteString("\n# HELP asura_incidents_total Total incidents by status.\n")
	sb.WriteString("# TYPE asura_incidents_total gauge\n")
	onePageMin := storage.Pagination{Page: 1, PerPage: 1}
	for _, st := range []string{incident.StatusOpen, incident.StatusAcknowledged, incident.StatusResolved} {
		res, err := h.store.ListIncidents(ctx, 0, st, "", onePageMin)
		if err != nil {
			continue
		}
		fmt.Fprintf(sb, "asura_incidents_total{status=\"%s\"} %d\n", st, res.Total)
	}
}

func (h *Handler) writeRequestMetrics(sb *strings.Builder, ctx context.Context) {
	now := time.Now().UTC()
	stats, err := h.store.GetRequestLogStats(ctx, now.Add(-24*time.Hour), now)
	if err != nil || stats == nil {
		return
	}

	sb.WriteString("\n# HELP asura_http_requests_24h Total HTTP requests in last 24 hours.\n")
	sb.WriteString("# TYPE asura_http_requests_24h gauge\n")
	fmt.Fprintf(sb, "asura_http_requests_24h %d\n", stats.TotalRequests)

	sb.WriteString("\n# HELP asura_http_unique_visitors_24h Unique visitors in last 24 hours.\n")
	sb.WriteString("# TYPE asura_http_unique_visitors_24h gauge\n")
	fmt.Fprintf(sb, "asura_http_unique_visitors_24h %d\n", stats.UniqueVisitors)

	sb.WriteString("\n# HELP asura_http_avg_latency_ms Average request latency in last 24 hours.\n")
	sb.WriteString("# TYPE asura_http_avg_latency_ms gauge\n")
	fmt.Fprintf(sb, "asura_http_avg_latency_ms %d\n", stats.AvgLatencyMs)
}

func formatTagsLabel(tags []storage.MonitorTag) string {
	if len(tags) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, t := range tags {
		label := strings.ReplaceAll(strings.ToLower(t.Name), " ", "_")
		val := t.Value
		if val == "" {
			val = "true"
		}
		fmt.Fprintf(&sb, ",tag_%s=\"%s\"", escapeProm(label), escapeProm(val))
	}
	return sb.String()
}

func escapeProm(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

func (h *Handler) MonitorMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)

	if f := r.URL.Query().Get("from"); f != "" {
		from, err = time.Parse(time.RFC3339, f)
		if err != nil {
			writeError(w, http.StatusBadRequest, "from must be an RFC3339 timestamp")
			return
		}
	}
	to := now
	if t := r.URL.Query().Get("to"); t != "" {
		to, err = time.Parse(time.RFC3339, t)
		if err != nil {
			writeError(w, http.StatusBadRequest, "to must be an RFC3339 timestamp")
			return
		}
	}
	if !from.Before(to) {
		writeError(w, http.StatusBadRequest, "from must precede to")
		return
	}
	data, err := h.store.GetMonitorMetricsSnapshot(r.Context(), id, from, to, httputil.MonitorAccess(r.Context(), "monitors.read")...)
	if err != nil {
		if monitorMutationError(w, err) {
			return
		}
		h.logger.Error("get monitor metrics", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get monitor metrics")
		return
	}
	var responseTime, checks any
	var responseSamples int64
	if data.Sampled {
		checks = map[string]int64{"total": data.Total, "up": data.Up, "down": data.Down, "degraded": data.Degraded}
		responseSamples = data.Response.Samples
		if responseSamples > 0 {
			responseTime = map[string]float64{"p50": data.Response.P50, "p95": data.Response.P95, "p99": data.Response.P99}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"monitor_id":       id,
		"from":             from.Format(time.RFC3339),
		"to":               to.Format(time.RFC3339),
		"sampled_metrics":  data.Sampled,
		"uptime_pct":       data.UptimePercent,
		"response_time":    responseTime,
		"response_samples": responseSamples,
		"checks":           checks,
	})
}

func (h *Handler) MonitorChart(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := h.requireMonitor(w, r, id, "monitors.read"); !ok {
		return
	}

	now := time.Now().UTC()
	var from time.Time
	switch r.URL.Query().Get("range") {
	case "1h":
		from = now.Add(-1 * time.Hour)
	case "6h":
		from = now.Add(-6 * time.Hour)
	case "7d":
		from = now.Add(-7 * 24 * time.Hour)
	case "30d":
		from = now.Add(-30 * 24 * time.Hour)
	default:
		from = now.Add(-24 * time.Hour)
	}

	points, err := h.store.GetResponseTimeSeries(r.Context(), id, from, now, 500, httputil.MonitorAccess(r.Context(), "monitors.read")...)
	if err != nil {
		if monitorMutationError(w, err) {
			return
		}
		h.logger.Error("get chart data", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get chart data")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"points": points,
	})
}

func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	up, down, degraded, paused, pending, err := h.store.CountMonitorsByStatus(r.Context(), httputil.MonitorAccess(r.Context(), "monitors.read")...)
	if err != nil {
		h.logger.Error("overview", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get overview")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"monitors": map[string]int64{
			"up":       up,
			"down":     down,
			"degraded": degraded,
			"paused":   paused,
			"pending":  pending,
			"total":    up + down + degraded + paused + pending,
		},
	})
}

package api

import (
	"net/http"
	"time"

	"github.com/y0f/asura/v3/internal/httputil"
)

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

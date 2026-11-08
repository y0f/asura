package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/y0f/asura/internal/storage"
)

const prometheusMaxBytes = 16 << 20
const prometheusTimeout = 10 * time.Second

var errPrometheusBudget = errors.New("metrics output exceeds budget")
var errPrometheusText = errors.New("metrics text is not valid UTF-8")

func (h *Handler) Metrics(w http.ResponseWriter, r *http.Request) {
	h.metrics(w, r, prometheusMaxBytes, prometheusTimeout)
}

func (h *Handler) metrics(w http.ResponseWriter, r *http.Request, maxBytes int, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	render := newPrometheusRenderer(maxBytes)
	now := time.Now().UTC()
	summary, err := h.store.VisitPrometheus(ctx, now.Add(-24*time.Hour), now, render.monitor)
	if err == nil && summary == nil {
		err = errors.New("missing metrics summary")
	}
	if err == nil {
		render.finish(summary)
		if h.pipeline != nil {
			b := &render.families[4]
			b.header("asura_scheduler_jobs_dropped_total", "Total scheduler jobs dropped due to full channel.", "counter")
			fmt.Fprintf(b, "asura_scheduler_jobs_dropped_total %d\n", h.pipeline.DroppedJobs())
			b.header("asura_notifications_dropped_total", "Total notification events dropped due to full channel.", "counter")
			fmt.Fprintf(b, "asura_notifications_dropped_total %d\n", h.pipeline.DroppedNotifications())
		}
		err = render.budget.err
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errPrometheusBudget) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			status = http.StatusServiceUnavailable
		}
		h.logger.Error("metrics snapshot failed", "error", err)
		writeError(w, status, "metrics unavailable")
		return
	}
	// All reads and encoding have succeeded. Never publish a partial healthy
	// scrape when a later query, deadline, or output budget fails.
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	for i := range render.families {
		if _, err := io.WriteString(w, render.families[i].text.String()); err != nil {
			return
		}
	}
}

type prometheusBudget struct {
	remaining int
	err       error
}

// All families share one limit, including headers and label expansion. Keep
// families separate until the complete snapshot succeeds to preserve grouping.
type prometheusBuffer struct {
	text   strings.Builder
	budget *prometheusBudget
}

func (b *prometheusBuffer) Write(p []byte) (int, error) {
	if b.budget.err != nil {
		return 0, b.budget.err
	}
	if len(p) > b.budget.remaining {
		b.budget.err = errPrometheusBudget
		return 0, b.budget.err
	}
	b.budget.remaining -= len(p)
	return b.text.Write(p)
}

func (b *prometheusBuffer) add(s string) {
	if b.budget.err != nil {
		return
	}
	if len(s) > b.budget.remaining {
		b.budget.err = errPrometheusBudget
		return
	}
	b.budget.remaining -= len(s)
	b.text.WriteString(s)
}

func (b *prometheusBuffer) header(name, help, kind string) {
	b.add("\n# HELP " + name + " " + help + "\n# TYPE " + name + " " + kind + "\n")
}

func (b *prometheusBuffer) quoted(value string) {
	if b.budget.err != nil {
		return
	}
	if len(value) > b.budget.remaining {
		b.budget.err = errPrometheusBudget
		return
	}
	if !utf8.ValidString(value) {
		b.budget.err = errPrometheusText
		return
	}
	b.add("\"")
	for {
		i := strings.IndexAny(value, "\\\"\n")
		if i < 0 {
			b.add(value)
			break
		}
		b.add(value[:i])
		switch value[i] {
		case '\\':
			b.add("\\\\")
		case '"':
			b.add("\\\"")
		case '\n':
			b.add("\\n")
		}
		value = value[i+1:]
	}
	b.add("\"")
}

func (b *prometheusBuffer) monitorLabels(row storage.PrometheusRow, withType bool) {
	fmt.Fprintf(b, "{id=\"%d\",name=", row.MonitorID)
	b.quoted(row.Name)
	if withType {
		b.add(",type=")
		b.quoted(row.Type)
	}
}

// Every non-alphanumeric UTF-8 byte, including underscore, has an unambiguous
// _XX encoding. Case is retained: distinct tag names cannot collapse to one
// label, and all results satisfy the legacy Prometheus label-name grammar.
func (b *prometheusBuffer) tag(tag *storage.MonitorTag) {
	b.add(",tag_")
	const hex = "0123456789ABCDEF"
	for i := 0; i < len(tag.Name) && b.budget.err == nil; i++ {
		c := tag.Name[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			b.add(tag.Name[i : i+1])
		} else {
			encoded := [3]byte{'_', hex[c>>4], hex[c&15]}
			_, _ = b.Write(encoded[:])
		}
	}
	b.add("=")
	value := tag.Value
	if value == "" {
		value = "true"
	}
	b.quoted(value)
}

type prometheusRenderer struct {
	budget          prometheusBudget
	families        [5]prometheusBuffer
	statuses, types map[string]int64
	lastID, lastUp  int64
	hasMonitor      bool
}

func newPrometheusRenderer(maxBytes int) *prometheusRenderer {
	r := &prometheusRenderer{
		budget:   prometheusBudget{remaining: maxBytes},
		statuses: map[string]int64{"up": 0, "down": 0, "degraded": 0, "paused": 0, "pending": 0},
		types:    make(map[string]int64),
	}
	for i := range r.families {
		r.families[i].budget = &r.budget
	}
	r.families[0].header("asura_monitor_up", "1 when the current monitor status is up; 0 otherwise.", "gauge")
	r.families[1].header("asura_monitor_response_time_ms", "Last observed active-check response time in milliseconds.", "gauge")
	r.families[2].header("asura_monitor_consecutive_failures", "Current consecutive failure count.", "gauge")
	r.families[3].header("asura_monitor_consecutive_successes", "Current consecutive success count.", "gauge")
	return r
}

func (r *prometheusRenderer) monitor(row storage.PrometheusRow) error {
	up := &r.families[0]
	if !r.hasMonitor || row.MonitorID != r.lastID {
		if r.hasMonitor {
			fmt.Fprintf(up, "} %d\n", r.lastUp)
		}
		r.hasMonitor, r.lastID, r.lastUp = true, row.MonitorID, 0
		if row.Status == "up" {
			r.lastUp = 1
		}
		up.add("asura_monitor_up")
		up.monitorLabels(row, true)
		if row.ResponseTime != nil {
			b := &r.families[1]
			b.add("asura_monitor_response_time_ms")
			b.monitorLabels(row, false)
			fmt.Fprintf(b, "} %d\n", *row.ResponseTime)
		}
		for i, metric := range []struct {
			name  string
			value int64
		}{
			{"asura_monitor_consecutive_failures", row.ConsecFails},
			{"asura_monitor_consecutive_successes", row.ConsecSuccesses},
		} {
			b := &r.families[i+2]
			b.add(metric.name)
			b.monitorLabels(row, false)
			fmt.Fprintf(b, "} %d\n", metric.value)
		}
		if r.budget.err != nil {
			return r.budget.err
		}
		status := row.Status
		switch status {
		case "up", "down", "degraded", "paused", "pending":
		default:
			status = "unknown"
		}
		r.statuses[status]++
		r.types[row.Type]++
	}
	if row.Tag != nil {
		up.tag(row.Tag)
	}
	return r.budget.err
}

func (r *prometheusRenderer) counts(name, help, label string, values map[string]int64) {
	b := &r.families[4]
	b.header(name, help, "gauge")
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		b.add(name + "{" + label + "=")
		b.quoted(key)
		fmt.Fprintf(b, "} %d\n", values[key])
	}
}

func (r *prometheusRenderer) finish(summary *storage.PrometheusSummary) {
	if r.hasMonitor {
		fmt.Fprintf(&r.families[0], "} %d\n", r.lastUp)
	}
	r.counts("asura_monitors_total", "Total number of monitors by status.", "status", r.statuses)
	r.counts("asura_monitors_by_type", "Total monitors by type.", "type", r.types)
	r.counts("asura_incidents_total", "Total incidents by status.", "status", map[string]int64{
		"open": summary.OpenIncidents, "acknowledged": summary.AcknowledgedIncidents, "resolved": summary.ResolvedIncidents,
	})
	b := &r.families[4]
	for _, metric := range []struct {
		name, help string
		value      int64
	}{
		{"asura_http_requests_24h", "Total HTTP requests in last 24 hours.", summary.Requests},
		{"asura_http_unique_visitors_24h", "Unique visitors in last 24 hours.", summary.UniqueVisitors},
		{"asura_http_avg_latency_ms", "Average request latency in last 24 hours.", summary.AvgLatencyMs},
	} {
		b.header(metric.name, metric.help, "gauge")
		fmt.Fprintf(b, "%s %d\n", metric.name, metric.value)
	}
}

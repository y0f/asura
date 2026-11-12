package analytics

import (
	"context"
	"time"

	"github.com/y0f/asura/v3/internal/storage"
)

// MonitorMetrics holds computed metrics for a monitor.
type MonitorMetrics struct {
	ResponseSamples int64   `json:"response_samples"`
	MonitorID       int64   `json:"monitor_id"`
	UptimePct       float64 `json:"uptime_pct"`
	P50             float64 `json:"p50"`
	P95             float64 `json:"p95"`
	P99             float64 `json:"p99"`
	TotalChecks     int64   `json:"total_checks"`
	UpChecks        int64   `json:"up_checks"`
	DownChecks      int64   `json:"down_checks"`
	DegradedChecks  int64   `json:"degraded_checks"`
}

// ComputeMetrics calculates metrics for a monitor over a time range.
func ComputeMetrics(ctx context.Context, store storage.Store, monitorID int64, from, to time.Time) (*MonitorMetrics, error) {
	data, err := store.GetMonitorMetricsSnapshot(ctx, monitorID, from, to)
	if err != nil {
		return nil, err
	}
	if !data.Sampled || data.UptimePercent == nil {
		return nil, storage.ErrNoMetricSamples
	}
	return &MonitorMetrics{
		MonitorID: monitorID,
		UptimePct: *data.UptimePercent,
		P50:       data.Response.P50, P95: data.Response.P95, P99: data.Response.P99,
		ResponseSamples: data.Response.Samples,
		TotalChecks:     data.Total, UpChecks: data.Up, DownChecks: data.Down, DegradedChecks: data.Degraded,
	}, nil
}

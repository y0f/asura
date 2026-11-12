package httputil

import (
	"context"

	"github.com/y0f/asura/v3/internal/sla"
)

// RedactSLAMonitorNames removes independently protected names from a newly
// computed, request-owned report. It does not change IDs or SLA measurements.
func RedactSLAMonitorNames(ctx context.Context, entries []*sla.ReportEntry) {
	key := GetAPIKey(ctx)
	for _, entry := range entries {
		if !key.AllowsMonitor("monitors.read", entry.MonitorID, entry.MonitorGroupID) {
			entry.MonitorName = ""
		}
	}
}

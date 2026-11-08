package httputil

import (
	"context"

	"github.com/y0f/asura/internal/redact"
	"github.com/y0f/asura/internal/storage"
)

// IncidentReadResponse preserves incident data without exposing independently
// protected monitor names or mutating the snapshot used for notifications.
func IncidentReadResponse(ctx context.Context, incident *storage.Incident) *storage.Incident {
	if incident == nil {
		return nil
	}
	response := *incident
	key := GetAPIKey(ctx)
	if !key.AllowsMonitor("incidents.read", incident.MonitorID, incident.MonitorGroupID) || !key.AllowsMonitor("diagnostics.read", incident.MonitorID, incident.MonitorGroupID) {
		response.Cause = redact.IncidentCause()
	}
	if !GetAPIKey(ctx).AllowsMonitor("monitors.read", incident.MonitorID, incident.MonitorGroupID) {
		response.MonitorName = ""
	}
	return &response
}

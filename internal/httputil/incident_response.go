package httputil

import (
	"context"

	"github.com/y0f/asura/internal/storage"
)

// IncidentReadResponse preserves incident data without exposing independently
// protected monitor names or mutating the snapshot used for notifications.
func IncidentReadResponse(ctx context.Context, incident *storage.Incident) *storage.Incident {
	if incident == nil {
		return nil
	}
	response := *incident
	if !GetAPIKey(ctx).AllowsMonitor("monitors.read", incident.MonitorID, incident.MonitorGroupID) {
		response.MonitorName = ""
	}
	return &response
}

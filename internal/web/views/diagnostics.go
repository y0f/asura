package views

import (
	"github.com/y0f/asura/v3/internal/redact"
	"github.com/y0f/asura/v3/internal/storage"
)

func (p LayoutParams) canReadDiagnostics(action string, monitorID int64, groupID *int64) bool {
	return p.CanMonitor(action, monitorID, groupID) && p.CanMonitor("diagnostics.read", monitorID, groupID)
}

func (p LayoutParams) incidentCause(incident *storage.Incident) string {
	if p.canReadDiagnostics("incidents.read", incident.MonitorID, incident.MonitorGroupID) {
		return incident.Cause
	}
	return redact.IncidentCause()
}

func (p IncidentDetailParams) eventMessage(event *storage.IncidentEvent) string {
	return redact.IncidentEvent(event, p.canReadDiagnostics("incidents.read", p.Incident.MonitorID, p.Incident.MonitorGroupID)).Message
}

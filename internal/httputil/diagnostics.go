package httputil

import (
	"context"
	"fmt"

	"github.com/y0f/asura/v3/internal/redact"
	"github.com/y0f/asura/v3/internal/storage"
)

// DiagnosticReadAccess must be passed to the actual data query after an
// authorized owner lookup. A later group move cannot keep the raw-data grant.
func DiagnosticReadAccess(ctx context.Context, action string, id int64, groupID *int64) ([]storage.MonitorAccess, bool) {
	key := GetAPIKey(ctx)
	diagnostics := key.AllowsMonitor(action, id, groupID) && key.AllowsMonitor("diagnostics.read", id, groupID)
	if diagnostics {
		return MonitorAccess(ctx, action, "diagnostics.read"), true
	}
	return MonitorAccess(ctx, action), false
}

func MonitorReadResponse(ctx context.Context, mon *storage.Monitor) *storage.Monitor {
	if mon == nil {
		return nil
	}
	key := GetAPIKey(ctx)
	return redact.MonitorWithDiagnostics(mon, key.AllowsMonitor("monitors.read", mon.ID, mon.GroupID) && key.AllowsMonitor("diagnostics.read", mon.ID, mon.GroupID))
}

func CheckResultsResponse(ctx context.Context, result *storage.PaginatedResult, diagnostics bool) (*storage.PaginatedResult, error) {
	return projectPage(result, func(check *storage.CheckResult) *storage.CheckResult {
		return redact.Check(check, diagnostics, GetAPIKey(ctx).HasPermission("agents.read"))
	})
}

func ContentChangesResponse(result *storage.PaginatedResult, diagnostics bool) (*storage.PaginatedResult, error) {
	return projectPage(result, func(change *storage.ContentChange) *storage.ContentChange {
		return redact.ContentChange(change, diagnostics)
	})
}

func IncidentListResponse(ctx context.Context, result *storage.PaginatedResult) (*storage.PaginatedResult, error) {
	return projectPage(result, func(incident *storage.Incident) *storage.Incident {
		return IncidentReadResponse(ctx, incident)
	})
}

func IncidentEventsResponse(events []*storage.IncidentEvent, diagnostics bool) []*storage.IncidentEvent {
	result := make([]*storage.IncidentEvent, len(events))
	for i, event := range events {
		result[i] = redact.IncidentEvent(event, diagnostics)
	}
	return result
}

func projectPage[T any](result *storage.PaginatedResult, project func(*T) *T) (*storage.PaginatedResult, error) {
	if result == nil {
		return nil, fmt.Errorf("missing read result")
	}
	rows, ok := result.Data.([]*T)
	if !ok && result.Data != nil {
		return nil, fmt.Errorf("unexpected read result")
	}
	copy := *result
	data := make([]*T, len(rows))
	for i, row := range rows {
		data[i] = project(row)
	}
	copy.Data = data
	return &copy, nil
}

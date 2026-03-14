package incident

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/y0f/asura/internal/storage"
)

// Manager handles incident lifecycle.
type Manager struct {
	store  storage.Store
	logger *slog.Logger
}

func NewManager(store storage.Store, logger *slog.Logger) *Manager {
	return &Manager{store: store, logger: logger}
}

// ProcessFailure checks if an incident should be created for the monitor.
// Returns the incident and whether it was newly created.
func (m *Manager) ProcessFailure(ctx context.Context, monitorID int64, monitorName, monitorStatus, cause string) (*storage.Incident, bool, error) {
	existing, err := m.store.GetOpenIncident(ctx, monitorID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	if existing != nil {
		m.store.InsertIncidentEvent(ctx, &storage.IncidentEvent{
			IncidentID: existing.ID,
			Type:       EventCheckFailed,
			Message:    cause,
		})
		return existing, false, nil
	}

	inc := &storage.Incident{
		MonitorID:   monitorID,
		MonitorName: monitorName,
		Status:      StatusOpen,
		Severity:    SeverityForStatus(monitorStatus),
		Cause:       cause,
	}
	if err := m.store.CreateIncident(ctx, inc); err != nil {
		return nil, false, err
	}

	m.store.InsertIncidentEvent(ctx, &storage.IncidentEvent{
		IncidentID: inc.ID,
		Type:       EventCreated,
		Message:    "Incident created: " + cause,
	})

	m.logger.Info("incident created", "incident_id", inc.ID, "monitor_id", monitorID, "cause", cause)
	return inc, true, nil
}

// ProcessRecovery checks if an open incident should be resolved.
// Returns the incident and whether it was resolved.
func (m *Manager) ProcessRecovery(ctx context.Context, monitorID int64) (*storage.Incident, bool, error) {
	existing, err := m.store.GetOpenIncident(ctx, monitorID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}

	if existing == nil {
		return nil, false, nil
	}

	now := time.Now().UTC()
	existing.Status = StatusResolved
	existing.ResolvedAt = &now
	existing.ResolvedBy = "auto"

	if err := m.store.UpdateIncident(ctx, existing); err != nil {
		return nil, false, err
	}

	m.store.InsertIncidentEvent(ctx, &storage.IncidentEvent{
		IncidentID: existing.ID,
		Type:       EventCheckRecovered,
		Message:    "Monitor recovered, incident auto-resolved",
	})

	m.logger.Info("incident resolved", "incident_id", existing.ID, "monitor_id", monitorID)
	return existing, true, nil
}

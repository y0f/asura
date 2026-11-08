package incident

import (
	"context"
	"log/slog"

	"github.com/y0f/asura/v2/internal/storage"
)

// Manager handles incident lifecycle.
type Manager struct {
	store  storage.Store
	logger *slog.Logger
}

func NewManager(store storage.Store, logger *slog.Logger) *Manager {
	return &Manager{store: store, logger: logger}
}

// ProcessFailure records a failure and creates or promotes the current open
// incident in one transaction. The stored monitor supplies its current name.
func (m *Manager) ProcessFailure(ctx context.Context, monitorID int64, _ string, monitorStatus, cause string) (*storage.Incident, bool, error) {
	inc, created, err := m.store.RecordIncidentFailure(ctx, monitorID, SeverityForStatus(monitorStatus), cause)
	if err != nil {
		return nil, false, err
	}
	if created {
		m.logger.Info("incident created", "incident_id", inc.ID, "monitor_id", monitorID, "cause", cause)
	}
	return inc, created, nil
}

// ProcessRecovery resolves the current open incident and records its timeline
// atomically. A concurrent manual resolution is a successful no-op.
func (m *Manager) ProcessRecovery(ctx context.Context, monitorID int64) (*storage.Incident, bool, error) {
	inc, resolved, err := m.store.RecordIncidentRecovery(ctx, monitorID)
	if err != nil {
		return nil, false, err
	}
	if resolved {
		m.logger.Info("incident resolved", "incident_id", inc.ID, "monitor_id", monitorID)
	}
	return inc, resolved, nil
}

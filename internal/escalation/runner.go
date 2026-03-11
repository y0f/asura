package escalation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/y0f/asura/internal/incident"
	"github.com/y0f/asura/internal/notifier"
	"github.com/y0f/asura/internal/storage"
)

// Runner periodically checks for pending escalation steps and fires notifications.
type Runner struct {
	store      storage.Store
	dispatcher *notifier.Dispatcher
	logger     *slog.Logger
	interval   time.Duration
	done       chan struct{}
}

// NewRunner creates a new escalation runner.
func NewRunner(store storage.Store, dispatcher *notifier.Dispatcher, logger *slog.Logger) *Runner {
	return &Runner{
		store:      store,
		dispatcher: dispatcher,
		logger:     logger,
		interval:   30 * time.Second,
		done:       make(chan struct{}),
	}
}

// Start launches the background ticker goroutine.
func (r *Runner) Start(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.done:
			return
		case <-ticker.C:
			r.Tick(ctx)
		}
	}
}

// Stop signals the runner to stop.
func (r *Runner) Stop() {
	select {
	case <-r.done:
	default:
		close(r.done)
	}
}

// Tick processes all pending escalation states. Exported for testing.
func (r *Runner) Tick(ctx context.Context) {
	states, err := r.store.ListPendingEscalationStates(ctx, time.Now())
	if err != nil {
		r.logger.Error("escalation: list pending states", "error", err)
		return
	}
	for _, state := range states {
		r.processState(ctx, state)
	}
}

func (r *Runner) processState(ctx context.Context, state *storage.EscalationState) {
	inc, err := r.store.GetIncident(ctx, state.IncidentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			r.deleteState(ctx, state.IncidentID)
		} else {
			r.logger.Error("escalation: get incident", "error", err, "incident_id", state.IncidentID)
		}
		return
	}

	if inc.Status == incident.StatusAcknowledged || inc.Status == incident.StatusResolved {
		r.deleteState(ctx, state.IncidentID)
		return
	}

	policy, err := r.store.GetEscalationPolicy(ctx, state.PolicyID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			r.deleteState(ctx, state.IncidentID)
		} else {
			r.logger.Error("escalation: get policy", "error", err, "policy_id", state.PolicyID)
		}
		return
	}

	if !policy.Enabled {
		r.deleteState(ctx, state.IncidentID)
		return
	}

	steps, err := r.store.GetEscalationPolicySteps(ctx, state.PolicyID)
	if err != nil {
		r.logger.Error("escalation: get steps", "error", err, "policy_id", state.PolicyID)
		return
	}

	if state.CurrentStep >= len(steps) {
		if policy.Repeat && len(steps) > 0 {
			state.CurrentStep = 0
		} else {
			r.deleteState(ctx, state.IncidentID)
			return
		}
	}

	step := steps[state.CurrentStep]

	mon, err := r.store.GetMonitor(ctx, inc.MonitorID)
	if err != nil {
		r.logger.Error("escalation: get monitor", "error", err, "monitor_id", inc.MonitorID)
		return
	}

	payload := &notifier.Payload{
		EventType:       "incident.escalated",
		Incident:        inc,
		Monitor:         mon,
		EscalationStep:  state.CurrentStep + 1,
		EscalationTotal: len(steps),
	}

	r.dispatcher.NotifyChannels(step.NotificationChannelIDs, payload)

	r.logger.Info("escalation step fired",
		"incident_id", state.IncidentID,
		"policy_id", state.PolicyID,
		"step", state.CurrentStep+1,
		"total_steps", len(steps),
	)

	nextStep := state.CurrentStep + 1
	if nextStep >= len(steps) {
		if policy.Repeat {
			nextStep = 0
			state.CurrentStep = nextStep
			state.NextFireAt = time.Now().Add(time.Duration(steps[0].DelayMinutes) * time.Minute)
		} else {
			r.deleteState(ctx, state.IncidentID)
			return
		}
	} else {
		state.CurrentStep = nextStep
		state.NextFireAt = time.Now().Add(time.Duration(steps[nextStep].DelayMinutes) * time.Minute)
	}

	if err := r.store.UpdateEscalationState(ctx, state); err != nil {
		r.logger.Error("escalation: update state", "error", err, "incident_id", state.IncidentID)
	}
}

func (r *Runner) deleteState(ctx context.Context, incidentID int64) {
	if err := r.store.DeleteEscalationStateByIncident(ctx, incidentID); err != nil {
		r.logger.Error("escalation: delete state", "error", err, "incident_id", incidentID)
	}
}

// StartEscalation creates an escalation state for a new incident if the monitor has an escalation policy.
func StartEscalation(ctx context.Context, store storage.Store, mon *storage.Monitor, incidentID int64, logger *slog.Logger) {
	if mon.EscalationPolicyID == nil {
		return
	}

	policy, err := store.GetEscalationPolicy(ctx, *mon.EscalationPolicyID)
	if err != nil || !policy.Enabled {
		return
	}

	steps, err := store.GetEscalationPolicySteps(ctx, policy.ID)
	if err != nil || len(steps) == 0 {
		return
	}

	state := &storage.EscalationState{
		IncidentID:  incidentID,
		PolicyID:    policy.ID,
		CurrentStep: 0,
		NextFireAt:  time.Now().Add(time.Duration(steps[0].DelayMinutes) * time.Minute),
	}
	if err := store.CreateEscalationState(ctx, state); err != nil {
		logger.Error("escalation: create state", "error", err, "incident_id", incidentID)
	}
}

// CancelEscalation removes the escalation state for an incident.
func CancelEscalation(ctx context.Context, store storage.Store, incidentID int64) {
	store.DeleteEscalationStateByIncident(ctx, incidentID)
}

// MarshalChannelIDs is a helper used by import/export.
func MarshalChannelIDs(ids []int64) string {
	b, _ := json.Marshal(ids)
	return string(b)
}

// UnmarshalChannelIDs is a helper used by import/export.
func UnmarshalChannelIDs(s string) []int64 {
	var ids []int64
	json.Unmarshal([]byte(s), &ids)
	return ids
}

// FormatStepSummary returns a human-readable summary like "3 steps, first at 0m".
func FormatStepSummary(steps []*storage.EscalationPolicyStep) string {
	if len(steps) == 0 {
		return "no steps"
	}
	if len(steps) == 1 {
		return fmt.Sprintf("1 step at %dm", steps[0].DelayMinutes)
	}
	return fmt.Sprintf("%d steps, first at %dm", len(steps), steps[0].DelayMinutes)
}

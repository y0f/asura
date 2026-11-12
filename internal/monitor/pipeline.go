package monitor

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/y0f/asura/v3/internal/anomaly"
	"github.com/y0f/asura/v3/internal/assertion"
	"github.com/y0f/asura/v3/internal/checker"
	"github.com/y0f/asura/v3/internal/diff"
	"github.com/y0f/asura/v3/internal/escalation"
	"github.com/y0f/asura/v3/internal/incident"
	"github.com/y0f/asura/v3/internal/storage"
)

// Pipeline orchestrates the full monitoring flow:
// Scheduler -> Workers -> Result Processor -> Incident Manager -> Notifications
type Pipeline struct {
	store                storage.Store
	registry             *checker.Registry
	incMgr               *incident.Manager
	logger               *slog.Logger
	scheduler            *Scheduler
	jobs                 chan Job
	results              chan WorkerResult
	notifyChan           chan NotificationEvent
	workers              int
	adaptiveIntervals    bool
	droppedNotifications atomic.Int64
	lastNotified         sync.Map     // map[int64]time.Time — tracks last resend per monitor
	slaStates            sync.Map     // map[int64]slaAlertState — guarded by each monitor's status lock
	statusLocks          monitorLocks // serializes status read-modify-write per monitor
	sources              sync.Map     // map[int64]monitorSources — per-location result streaks (guarded by statusLocks)
	sourceCheckedAt      sync.Map     // map[int64]map[string]time.Time — newest applied checked_at per location (guarded by statusLocks)
	sourceRevision       sync.Map     // map[int64]int64 — configuration shared by source caches (guarded by statusLocks)
	incidentOpen         sync.Map     // map[int64]bool — false once a recovery pass found no open incident; absent = unknown
}

// lockMonitorStatus serializes the monitor_status read-modify-write for a
// single monitor so concurrent writers (the local worker and remote agent
// result handlers) cannot lose counter increments. It returns the unlock func.
func (p *Pipeline) lockMonitorStatus(monitorID int64) func() {
	return p.statusLocks.lock(monitorID)
}

// NotificationEvent is emitted when something noteworthy happens.
type NotificationEvent struct {
	EventType string
	MonitorID int64
	Incident  *storage.Incident
	Monitor   *storage.Monitor
	Change    *storage.ContentChange
}

func NewPipeline(store storage.Store, registry *checker.Registry, incMgr *incident.Manager, workers int, adaptiveIntervals bool, logger *slog.Logger) *Pipeline {
	jobs := make(chan Job, workers*2)
	results := make(chan WorkerResult, workers*2)
	notifyChan := make(chan NotificationEvent, 100)

	p := &Pipeline{
		store:             store,
		registry:          registry,
		incMgr:            incMgr,
		logger:            logger,
		scheduler:         NewScheduler(store, jobs, logger),
		jobs:              jobs,
		results:           results,
		notifyChan:        notifyChan,
		workers:           workers,
		adaptiveIntervals: adaptiveIntervals,
	}
	p.scheduler.afterLoad = p.pruneDeletedMonitorCaches
	return p
}

// NotifyChan returns the channel for notification events.
func (p *Pipeline) NotifyChan() <-chan NotificationEvent {
	return p.notifyChan
}

// ReloadMonitors triggers a scheduler reload.
func (p *Pipeline) ReloadMonitors() {
	p.scheduler.TriggerReload()
}

// DroppedJobs returns the total number of scheduler jobs dropped due to a full channel.
func (p *Pipeline) DroppedJobs() int64 {
	return p.scheduler.droppedJobs.Load()
}

// DroppedNotifications returns the total number of notification events dropped due to a full channel.
func (p *Pipeline) DroppedNotifications() int64 {
	return p.droppedNotifications.Load()
}

func (p *Pipeline) Run(ctx context.Context) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var running sync.WaitGroup
	running.Go(func() { p.scheduler.Run(runCtx) })
	running.Go(func() { p.reconcilePendingAnnouncements(runCtx) })
	running.Go(func() { p.reconcileSLABudgets(runCtx) })

	pool := NewPool(p.workers, p.registry, p.jobs, p.results, p.logger)
	running.Go(func() { pool.Run(runCtx) })

	p.processResults(runCtx)
	cancel()
	running.Wait()
}

func (p *Pipeline) processResults(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case wr, ok := <-p.results:
			if !ok {
				return
			}
			p.handleResult(ctx, wr)
		}
	}
}

func (p *Pipeline) handleResult(ctx context.Context, wr WorkerResult) {
	mon := wr.Monitor
	defer p.scheduler.complete(mon.ID)

	if wr.Err != nil {
		p.logger.Error("check error", "monitor_id", mon.ID, "error", wr.Err)
		wr.Result = &checker.Result{
			Status:  "down",
			Message: wr.Err.Error(),
		}
	}

	result := wr.Result
	finalStatus := applyUpsideDown(mon, evaluateAssertions(mon, result), result)

	now := time.Now()
	unlock := p.lockMonitorStatus(mon.ID)
	defer unlock()

	status, err := p.loadMonitorStatus(ctx, mon)
	if err != nil {
		p.logger.Error("read monitor status", "monitor_id", mon.ID, "error", err)
		return
	}

	// The anomaly downgrade must happen before the check result is stored so
	// history, uptime and incident counting all see the same status.
	if finalStatus == "up" {
		if msg, ok := anomalyMessage(mon, status, result.ResponseTime); ok {
			finalStatus = "degraded"
			result.Message = msg
		}
	}

	cr := buildCheckResult(mon, result, finalStatus)

	// Capture the previous check's body before inserting the new row, so the
	// content diff compares against the prior body (not the one just stored).
	// The (up to 1MB) body is only loaded when the hash actually changed.
	var prevBody string
	bodyChanged := mon.TrackChanges && result.BodyHash != "" &&
		status.LastBodyHash != "" && status.LastBodyHash != result.BodyHash
	if bodyChanged {
		if prev, err := p.store.GetLatestCheckResult(ctx, mon.ID); err == nil && prev != nil {
			prevBody = prev.Body
		}
	}

	prepared := p.prepareSourceResult(mon, status, localSource, finalStatus, cr.Message, now, now)
	status.LastCheckAt = &now
	status.ResponseTime = cr.ResponseTime

	oldBodyHash := status.LastBodyHash
	if result.BodyHash != "" {
		status.LastBodyHash = result.BodyHash
	}
	certChanged := updateCertFingerprint(status, result.CertFingerprint)
	if err := p.store.RecordMonitorCheck(ctx, mon, cr, status); err != nil {
		if !errors.Is(err, storage.ErrStaleMonitorCheck) {
			p.logger.Error("record monitor check", "monitor_id", mon.ID, "error", err)
		}
		return
	}
	p.commitSourceResult(mon, prepared)
	if bodyChanged {
		p.handleContentChange(ctx, mon, oldBodyHash, result.BodyHash, prevBody, result.Body)
	}
	if certChanged {
		p.emitNotification(ctx, "cert.changed", nil, mon, nil)
	}

	if p.adaptiveIntervals {
		baseInterval := time.Duration(mon.Interval) * time.Second
		prevMultiplier := p.scheduler.GetMultiplier(mon.ID)
		newInterval, _ := computeAdaptiveInterval(baseInterval, status.ConsecSuccesses, status.ConsecFails, prevMultiplier)
		p.scheduler.UpdateInterval(mon.ID, newInterval)
	}

	p.processIncidents(ctx, mon, status.Status, status, prepared.message)
}

// ProcessAgentResult handles a check result submitted by a remote agent.
// The raw agent status goes through the same assertion, upside_down and
// anomaly evaluation as local results (cr.Status and cr.Message are updated
// in place) before it is stored, so history, uptime and incidents all see the
// same status. It is tracked as its own location so it cannot reset the
// streaks of the local worker or of other agents.
//
// cr.CreatedAt is when the agent ran the check (zero or a future time means
// now). A result checked before the newest one already applied from the same
// agent is stored but does not change the monitor's status or incidents.
// There is no absolute age limit, so an agent with a long cycle that posts
// minutes after checking is still applied.
func (p *Pipeline) ProcessAgentResult(ctx context.Context, mon *storage.Monitor, cr *storage.CheckResult) error {
	if mon == nil || cr == nil || cr.AgentID == nil || *cr.AgentID <= 0 {
		return storage.ErrStaleMonitorCheck
	}
	unlock := p.lockMonitorStatus(mon.ID)
	defer unlock()
	now := time.Now()
	checkedAt := cr.CreatedAt
	if checkedAt.IsZero() || checkedAt.After(now) {
		checkedAt = now
	}

	status, err := p.loadMonitorStatus(ctx, mon)
	if err != nil {
		return err
	}
	// Assertions and anomaly evaluation must not mutate caller-owned output
	// when the commit is rejected or any database write fails.
	saved := *cr
	finalStatus := evaluateAgentResult(mon, &saved, status)

	source := agentSource(cr)
	if p.staleSourceResult(mon, source, checkedAt) {
		if err := p.store.RecordMonitorCheck(ctx, mon, &saved, nil); err != nil {
			return err
		}
		*cr = saved
		return nil
	}

	prepared := p.prepareSourceResult(mon, status, source, finalStatus, saved.Message, checkedAt, now)
	status.LastCheckAt = &now
	status.ResponseTime = saved.ResponseTime
	certChanged := updateCertFingerprint(status, saved.CertFingerprint)
	if err := p.store.RecordMonitorCheck(ctx, mon, &saved, status); err != nil {
		return err
	}
	*cr = saved
	p.commitSourceResult(mon, prepared)
	if certChanged {
		p.emitNotification(ctx, "cert.changed", nil, mon, nil)
	}
	p.processIncidents(ctx, mon, status.Status, status, prepared.message)
	return nil
}

func (p *Pipeline) loadMonitorStatus(ctx context.Context, mon *storage.Monitor) (*storage.MonitorStatus, error) {
	status, err := p.store.GetMonitorStatus(ctx, mon.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return &storage.MonitorStatus{MonitorID: mon.ID, ConfigRevision: mon.ConfigRevision}, nil
	}
	if err != nil {
		return nil, err
	}
	if status == nil {
		return nil, errors.New("monitor status returned no row without an error")
	}
	copy := *status
	if copy.ObservationKind != storage.ObservationProbe {
		// Legacy and passive state cannot seed comparisons against a new probe.
		// Keep these changes staged until the result transaction succeeds.
		copy.LastBodyHash, copy.LastCertFingerprint = "", ""
		copy.BaselineAvg, copy.BaselineStddev, copy.BaselineUpdatedAt = 0, 0, nil
	}
	if copy.ConfigRevision > 0 && copy.ConfigRevision != mon.ConfigRevision {
		copy.ConsecFails, copy.ConsecSuccesses = 0, 0
	}
	copy.ConfigRevision = mon.ConfigRevision
	return &copy, nil
}

func updateCertFingerprint(status *storage.MonitorStatus, fp string) bool {
	if fp == "" {
		return false
	}
	changed := status.LastCertFingerprint != "" && status.LastCertFingerprint != fp
	status.LastCertFingerprint = fp
	return changed
}

// applyUpsideDown inverts the status of upside-down monitors.
func applyUpsideDown(mon *storage.Monitor, status string, result *checker.Result) string {
	if !mon.UpsideDown {
		return status
	}
	if status == "up" {
		return "down"
	}
	result.Message = ""
	return "up"
}

// anomalyMessage reports whether responseTime is anomalous against the
// monitor's stored baseline, with a human-readable explanation.
func anomalyMessage(mon *storage.Monitor, status *storage.MonitorStatus, responseTime int64) (string, bool) {
	if mon.AnomalySensitivity == "" || mon.AnomalySensitivity == "off" || status.BaselineAvg <= 0 {
		return "", false
	}
	if !anomaly.IsAnomaly(responseTime, status.BaselineAvg, status.BaselineStddev, mon.AnomalySensitivity) {
		return "", false
	}
	return fmt.Sprintf("response time %dms exceeds baseline %.0fms (%.1fσ)",
		responseTime, status.BaselineAvg, (float64(responseTime)-status.BaselineAvg)/status.BaselineStddev), true
}

func evaluateAssertions(mon *storage.Monitor, result *checker.Result) string {
	return evaluateAssertionSet(mon.Assertions, result)
}

func evaluateAssertionSet(assertions json.RawMessage, result *checker.Result) string {
	finalStatus := result.Status
	if len(assertions) == 0 || string(assertions) == "[]" {
		return finalStatus
	}
	assertionResult := assertion.Evaluate(assertions, result.StatusCode, result.Body,
		result.Headers, result.ResponseTime, result.CertExpiry, result.DNSRecords)
	if !assertionResult.Pass {
		// A failing "degraded" assertion must not soften a hard failure.
		if assertionResult.Degraded && finalStatus != "down" {
			finalStatus = "degraded"
		} else {
			finalStatus = "down"
		}
		if result.Message == "" {
			result.Message = assertionResult.Message
		} else {
			result.Message += "; " + assertionResult.Message
		}
	}
	return finalStatus
}

func buildCheckResult(mon *storage.Monitor, result *checker.Result, finalStatus string) *storage.CheckResult {
	headersJSON, _ := json.Marshal(result.Headers)
	dnsJSON, _ := json.Marshal(result.DNSRecords)

	var certExpiry *time.Time
	if result.CertExpiry != nil {
		t := time.Unix(*result.CertExpiry, 0)
		certExpiry = &t
	}

	// Persist the body only for change-tracking monitors; otherwise it grows
	// check_results unbounded with data nothing reads. The diff path relies on
	// the stored body, which remains available for tracking monitors.
	body := ""
	if mon.TrackChanges {
		body = result.Body
	}

	return &storage.CheckResult{
		MonitorID:       mon.ID,
		Status:          finalStatus,
		ResponseTime:    result.ResponseTime,
		StatusCode:      result.StatusCode,
		Message:         result.Message,
		Headers:         string(headersJSON),
		Body:            body,
		BodyHash:        result.BodyHash,
		CertExpiry:      certExpiry,
		CertFingerprint: result.CertFingerprint,
		DNSRecords:      string(dnsJSON),
	}
}

func (p *Pipeline) processIncidents(ctx context.Context, mon *storage.Monitor, finalStatus string, status *storage.MonitorStatus, message string) {
	switch {
	case finalStatus != "up" && status.ConsecFails >= mon.FailureThreshold:
		p.processFailure(ctx, mon, finalStatus, message, p.inMaintenance(ctx, mon.ID))
	case finalStatus == "up" && status.ConsecSuccesses >= mon.SuccessThreshold && p.mayHaveOpenIncident(mon.ID):
		p.processRecovery(ctx, mon)
	}
}

func (p *Pipeline) inMaintenance(ctx context.Context, monitorID int64) bool {
	in, _ := p.store.IsMonitorInMaintenance(ctx, monitorID, time.Now())
	return in
}

// mayHaveOpenIncident lets steady healthy checks skip the recovery queries
// once a recovery pass found no open incident for the monitor.
func (p *Pipeline) mayHaveOpenIncident(monitorID int64) bool {
	v, ok := p.incidentOpen.Load(monitorID)
	return !ok || v.(bool)
}

func (p *Pipeline) processFailure(ctx context.Context, mon *storage.Monitor, monitorStatus, message string, inMaintenance bool) error {
	inc, created, err := p.incMgr.ProcessFailure(ctx, mon.ID, mon.Name, monitorStatus, message)
	if err != nil {
		p.logger.Error("process failure", "error", err)
		return err
	}
	p.incidentOpen.Store(mon.ID, true)
	if inMaintenance {
		return nil
	}
	if created || inc.AnnouncementPending {
		p.announceIncident(ctx, mon, inc)
	} else {
		p.prepareEscalation(ctx, mon, inc)
		if p.shouldResend(mon) {
			p.emitNotification(ctx, "incident.reminder", inc, mon, nil)
			p.lastNotified.Store(mon.ID, time.Now())
		}
	}
	p.checkSLABreach(ctx, mon)
	return nil
}

// announceIncident emits incident.created and starts escalation.
func (p *Pipeline) announceIncident(ctx context.Context, mon *storage.Monitor, inc *storage.Incident) {
	if !p.emitNotification(ctx, "incident.created", inc, mon, nil) {
		return
	}
	p.lastNotified.Store(mon.ID, time.Now())
	if err := p.store.MarkIncidentAnnounced(ctx, inc.ID); err != nil {
		p.logger.Error("mark incident announced", "incident_id", inc.ID, "error", err)
	}
	p.prepareEscalation(ctx, mon, inc)
	p.checkSLABreach(ctx, mon)
}

func (p *Pipeline) prepareEscalation(ctx context.Context, mon *storage.Monitor, inc *storage.Incident) {
	if inc.EscalationPending {
		if err := escalation.StartEscalation(ctx, p.store, mon, inc.ID, p.logger); err != nil {
			p.logger.Error("start incident escalation", "incident_id", inc.ID, "error", err)
		}
	}
}

func (p *Pipeline) processRecovery(ctx context.Context, mon *storage.Monitor) error {
	inc, resolved, err := p.incMgr.ProcessRecovery(ctx, mon.ID)
	if err != nil {
		p.logger.Error("process recovery", "error", err)
		return err
	}
	p.incidentOpen.Store(mon.ID, false)
	if resolved {
		p.notifyResolved(ctx, mon, inc, p.inMaintenance(ctx, mon.ID))
	}
	p.lastNotified.Delete(mon.ID)
	return nil
}

// notifyResolved cancels escalation and emits incident.resolved, unless the
// monitor is in maintenance or the incident was never announced.
func (p *Pipeline) notifyResolved(ctx context.Context, mon *storage.Monitor, inc *storage.Incident, inMaintenance bool) {
	escalation.CancelEscalation(ctx, p.store, inc.ID)
	if inc.AnnouncementPending || inMaintenance {
		return
	}
	p.emitNotification(ctx, "incident.resolved", inc, mon, nil)
}

func (p *Pipeline) shouldResend(mon *storage.Monitor) bool {
	if mon.ResendInterval <= 0 {
		return false
	}
	v, ok := p.lastNotified.Load(mon.ID)
	if !ok {
		return true
	}
	return time.Since(v.(time.Time)) >= time.Duration(mon.ResendInterval)*time.Second
}

func (p *Pipeline) handleContentChange(ctx context.Context, mon *storage.Monitor, oldHash, newHash, oldBody, newBody string) {
	diffText := diff.Compute(oldBody, newBody)

	change := &storage.ContentChange{
		MonitorID: mon.ID,
		OldHash:   oldHash,
		NewHash:   newHash,
		Diff:      diffText,
		OldBody:   oldBody,
		NewBody:   newBody,
	}

	if err := p.store.InsertContentChange(ctx, change); err != nil {
		p.logger.Error("insert content change", "error", err)
		return
	}

	p.emitNotification(ctx, "content.changed", nil, mon, change)
}

// criticalNotification reports whether an event is a state-transition that must
// not be dropped under load (mass outages fill the buffer fastest exactly when
// these events matter most).
func criticalNotification(eventType string) bool {
	switch eventType {
	case "incident.created", "incident.resolved", "sla.breach":
		return true
	default:
		return false
	}
}

func (p *Pipeline) emitNotification(ctx context.Context, eventType string, inc *storage.Incident, mon *storage.Monitor, change *storage.ContentChange) bool {
	var monitorID int64
	if mon != nil {
		monitorID = mon.ID
	} else if inc != nil {
		monitorID = inc.MonitorID
	} else if change != nil {
		monitorID = change.MonitorID
	}
	ev := NotificationEvent{
		EventType: eventType,
		MonitorID: monitorID,
		Incident:  inc,
		Monitor:   mon,
		Change:    change,
	}

	if criticalNotification(eventType) {
		// Prefer delivery of state-transition events, but bound the wait so a
		// slow consumer cannot stall the single result-processing goroutine
		// (and thus every monitor) during a mass outage.
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case p.notifyChan <- ev:
			return true
		case <-ctx.Done():
			p.droppedNotifications.Add(1)
			p.logger.Warn("notification channel send aborted", "event", eventType, "error", ctx.Err())
		case <-timer.C:
			p.droppedNotifications.Add(1)
			p.logger.Warn("notification channel full, dropped event after timeout", "event", eventType)
		}
		return false
	}

	select {
	case p.notifyChan <- ev:
		return true
	default:
		p.droppedNotifications.Add(1)
		p.logger.Warn("notification channel full, dropping event", "event", eventType)
		return false
	}
}

// ProcessHeartbeatPing serializes ping/recovery with expiry and incident creation.
// Every accepted ping updates the dashboard, including the first pending ping.
func (p *Pipeline) ProcessHeartbeatPing(ctx context.Context, monitorID int64, token string) error {
	unlock := p.lockMonitorStatus(monitorID)
	defer unlock()
	mon, err := p.store.GetMonitor(ctx, monitorID)
	if err != nil {
		return err
	}
	if err := p.store.UpdateHeartbeatPing(ctx, monitorID, token); err != nil {
		return err
	}
	if p.mayHaveOpenIncident(mon.ID) {
		return p.processRecovery(ctx, mon)
	}
	return nil
}

// ProcessManualStatus handles a user-set status change for a manual monitor.
func (p *Pipeline) ProcessManualStatus(ctx context.Context, mon *storage.Monitor, newStatus, message string, access ...storage.MonitorAccess) error {
	unlock := p.lockMonitorStatus(mon.ID)
	defer unlock()
	mon, err := p.store.RecordManualStatus(ctx, mon.ID, newStatus, message, access...)
	if err != nil {
		return err
	}

	inMaintenance := p.inMaintenance(ctx, mon.ID)

	if newStatus == "up" {
		inc, resolved, err := p.incMgr.ProcessRecovery(ctx, mon.ID)
		if err != nil {
			p.logger.Error("manual status: process recovery", "error", err)
			return err
		}
		p.incidentOpen.Store(mon.ID, false)
		if resolved {
			p.notifyResolved(ctx, mon, inc, inMaintenance)
		}
		p.lastNotified.Delete(mon.ID)
		return nil
	}
	// Continued failures may raise severity or announce an incident after maintenance.
	return p.processFailure(ctx, mon, newStatus, message, inMaintenance)
}

func HashBody(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:])
}

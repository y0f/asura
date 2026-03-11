package monitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/y0f/asura/internal/assertion"
	"github.com/y0f/asura/internal/checker"
	"github.com/y0f/asura/internal/diff"
	"github.com/y0f/asura/internal/escalation"
	"github.com/y0f/asura/internal/incident"
	"github.com/y0f/asura/internal/sla"
	"github.com/y0f/asura/internal/storage"
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
	lastNotified         sync.Map // map[int64]time.Time — tracks last resend per monitor
	lastSLABreach        sync.Map // map[int64]time.Time — tracks last SLA breach alert per monitor
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

	return &Pipeline{
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
	// Start scheduler
	go p.scheduler.Run(ctx)

	// Start worker pool
	pool := NewPool(p.workers, p.registry, p.jobs, p.results, p.logger)
	go pool.Run(ctx)

	// Start result processor
	p.processResults(ctx)
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

	if wr.Err != nil {
		p.logger.Error("check error", "monitor_id", mon.ID, "error", wr.Err)
		wr.Result = &checker.Result{
			Status:  "down",
			Message: wr.Err.Error(),
		}
	}

	result := wr.Result
	finalStatus := evaluateAssertions(mon, result)

	if mon.UpsideDown {
		if finalStatus == "up" {
			finalStatus = "down"
		} else {
			finalStatus = "up"
			result.Message = ""
		}
	}

	cr := buildCheckResult(mon, result, finalStatus)

	if err := p.store.InsertCheckResult(ctx, cr); err != nil {
		p.logger.Error("insert check result", "error", err)
		return
	}

	now := time.Now()
	status, err := p.store.GetMonitorStatus(ctx, mon.ID)
	if err != nil {
		p.logger.Warn("get monitor status, using defaults", "monitor_id", mon.ID, "error", err)
		status = &storage.MonitorStatus{MonitorID: mon.ID}
	}

	status.Status = finalStatus
	status.LastCheckAt = &now

	if finalStatus == "up" {
		status.ConsecSuccesses++
		status.ConsecFails = 0
	} else {
		status.ConsecFails++
		status.ConsecSuccesses = 0
	}

	if mon.TrackChanges && result.BodyHash != "" {
		oldHash := status.LastBodyHash
		if oldHash != "" && oldHash != result.BodyHash {
			p.handleContentChange(ctx, mon, oldHash, result.BodyHash, result.Body, status)
		}
		status.LastBodyHash = result.BodyHash
	} else if result.BodyHash != "" {
		status.LastBodyHash = result.BodyHash
	}

	if result.CertFingerprint != "" {
		oldFP := status.LastCertFingerprint
		if oldFP != "" && oldFP != result.CertFingerprint {
			p.emitNotification("cert.changed", nil, mon, nil)
		}
		status.LastCertFingerprint = result.CertFingerprint
	}

	if err := p.store.UpsertMonitorStatus(ctx, status); err != nil {
		p.logger.Error("upsert monitor status", "error", err)
	}

	if p.adaptiveIntervals {
		baseInterval := time.Duration(mon.Interval) * time.Second
		prevMultiplier := p.scheduler.GetMultiplier(mon.ID)
		newInterval, _ := computeAdaptiveInterval(baseInterval, status.ConsecSuccesses, status.ConsecFails, prevMultiplier)
		p.scheduler.UpdateInterval(mon.ID, newInterval)
	}

	p.processIncidents(ctx, mon, finalStatus, status, cr.Message)
}

func evaluateAssertions(mon *storage.Monitor, result *checker.Result) string {
	finalStatus := result.Status
	if len(mon.Assertions) == 0 || string(mon.Assertions) == "[]" {
		return finalStatus
	}
	assertionResult := assertion.Evaluate(mon.Assertions, result.StatusCode, result.Body,
		result.Headers, result.ResponseTime, result.CertExpiry, result.DNSRecords)
	if !assertionResult.Pass {
		if assertionResult.Degraded {
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

	return &storage.CheckResult{
		MonitorID:       mon.ID,
		Status:          finalStatus,
		ResponseTime:    result.ResponseTime,
		StatusCode:      result.StatusCode,
		Message:         result.Message,
		Headers:         string(headersJSON),
		Body:            result.Body,
		BodyHash:        result.BodyHash,
		CertExpiry:      certExpiry,
		CertFingerprint: result.CertFingerprint,
		DNSRecords:      string(dnsJSON),
	}
}

func (p *Pipeline) processIncidents(ctx context.Context, mon *storage.Monitor, finalStatus string, status *storage.MonitorStatus, message string) {
	inMaintenance, _ := p.store.IsMonitorInMaintenance(ctx, mon.ID, time.Now())

	if finalStatus != "up" && status.ConsecFails >= mon.FailureThreshold {
		p.processFailure(ctx, mon, message, inMaintenance)
	} else if finalStatus == "up" && status.ConsecSuccesses >= mon.SuccessThreshold {
		p.processRecovery(ctx, mon, inMaintenance)
	}
}

func (p *Pipeline) processFailure(ctx context.Context, mon *storage.Monitor, message string, inMaintenance bool) {
	inc, created, err := p.incMgr.ProcessFailure(ctx, mon.ID, mon.Name, message)
	if err != nil {
		p.logger.Error("process failure", "error", err)
		return
	}
	if inMaintenance {
		return
	}
	if created {
		p.emitNotification("incident.created", inc, mon, nil)
		p.lastNotified.Store(mon.ID, time.Now())
		escalation.StartEscalation(ctx, p.store, mon, inc.ID, p.logger)
		p.checkSLABreach(ctx, mon)
	} else if p.shouldResend(mon) {
		p.emitNotification("incident.reminder", inc, mon, nil)
		p.lastNotified.Store(mon.ID, time.Now())
	}
}

func (p *Pipeline) processRecovery(ctx context.Context, mon *storage.Monitor, inMaintenance bool) {
	inc, resolved, err := p.incMgr.ProcessRecovery(ctx, mon.ID)
	if err != nil {
		p.logger.Error("process recovery", "error", err)
		return
	}
	if resolved {
		escalation.CancelEscalation(ctx, p.store, inc.ID)
		if !inMaintenance {
			p.emitNotification("incident.resolved", inc, mon, nil)
		}
	}
	p.lastNotified.Delete(mon.ID)
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

func (p *Pipeline) checkSLABreach(ctx context.Context, mon *storage.Monitor) {
	if mon.SLATarget <= 0 {
		return
	}
	if v, ok := p.lastSLABreach.Load(mon.ID); ok {
		if time.Since(v.(time.Time)) < time.Hour {
			return
		}
	}
	status, err := sla.Compute(ctx, p.store, mon.ID, mon.SLATarget)
	if err != nil {
		return
	}
	if status.Breached || status.BudgetRemainPct <= 10 {
		p.emitNotification("sla.breach", nil, mon, nil)
		p.lastSLABreach.Store(mon.ID, time.Now())
	}
}

func (p *Pipeline) handleContentChange(ctx context.Context, mon *storage.Monitor, oldHash, newHash, newBody string, status *storage.MonitorStatus) {
	oldBody := ""
	latest, err := p.store.GetLatestCheckResult(ctx, mon.ID)
	if err == nil && latest != nil {
		oldBody = latest.Body
	}

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

	p.emitNotification("content.changed", nil, mon, change)
}

func (p *Pipeline) emitNotification(eventType string, inc *storage.Incident, mon *storage.Monitor, change *storage.ContentChange) {
	var monitorID int64
	if mon != nil {
		monitorID = mon.ID
	} else if inc != nil {
		monitorID = inc.MonitorID
	} else if change != nil {
		monitorID = change.MonitorID
	}
	select {
	case p.notifyChan <- NotificationEvent{
		EventType: eventType,
		MonitorID: monitorID,
		Incident:  inc,
		Monitor:   mon,
		Change:    change,
	}:
	default:
		p.droppedNotifications.Add(1)
		p.logger.Warn("notification channel full, dropping event", "event", eventType)
	}
}

// ProcessHeartbeatRecovery handles recovery when a heartbeat ping is received for a down monitor.
func (p *Pipeline) ProcessHeartbeatRecovery(ctx context.Context, mon *storage.Monitor) {
	now := time.Now()
	status := &storage.MonitorStatus{
		MonitorID:       mon.ID,
		Status:          "up",
		LastCheckAt:     &now,
		ConsecSuccesses: mon.SuccessThreshold,
		ConsecFails:     0,
	}
	if err := p.store.UpsertMonitorStatus(ctx, status); err != nil {
		p.logger.Error("heartbeat recovery: upsert status", "error", err)
	}

	inMaintenance, _ := p.store.IsMonitorInMaintenance(ctx, mon.ID, now)
	inc, resolved, err := p.incMgr.ProcessRecovery(ctx, mon.ID)
	if err != nil {
		p.logger.Error("heartbeat recovery: process recovery", "error", err)
		return
	}
	if resolved {
		escalation.CancelEscalation(ctx, p.store, inc.ID)
		if !inMaintenance {
			p.emitNotification("incident.resolved", inc, mon, nil)
		}
	}
}

// ProcessManualStatus handles a user-set status change for a manual monitor.
func (p *Pipeline) ProcessManualStatus(ctx context.Context, mon *storage.Monitor, newStatus, message string) {
	now := time.Now()

	cr := &storage.CheckResult{
		MonitorID: mon.ID,
		Status:    newStatus,
		Message:   message,
	}
	if err := p.store.InsertCheckResult(ctx, cr); err != nil {
		p.logger.Error("manual status: insert check result", "error", err)
		return
	}

	status, err := p.store.GetMonitorStatus(ctx, mon.ID)
	if err != nil {
		status = &storage.MonitorStatus{MonitorID: mon.ID}
	}

	prevStatus := status.Status
	status.Status = newStatus
	status.LastCheckAt = &now

	if newStatus == "up" {
		status.ConsecSuccesses = mon.SuccessThreshold
		status.ConsecFails = 0
	} else {
		status.ConsecFails = mon.FailureThreshold
		status.ConsecSuccesses = 0
	}

	if err := p.store.UpsertMonitorStatus(ctx, status); err != nil {
		p.logger.Error("manual status: upsert status", "error", err)
	}

	inMaintenance, _ := p.store.IsMonitorInMaintenance(ctx, mon.ID, now)

	if newStatus == "up" && prevStatus != "up" {
		inc, resolved, err := p.incMgr.ProcessRecovery(ctx, mon.ID)
		if err != nil {
			p.logger.Error("manual status: process recovery", "error", err)
			return
		}
		if resolved {
			escalation.CancelEscalation(ctx, p.store, inc.ID)
			if !inMaintenance {
				p.emitNotification("incident.resolved", inc, mon, nil)
			}
		}
	} else if newStatus != "up" && (prevStatus == "up" || prevStatus == "" || prevStatus == "pending") {
		inc, created, err := p.incMgr.ProcessFailure(ctx, mon.ID, mon.Name, message)
		if err != nil {
			p.logger.Error("manual status: process failure", "error", err)
			return
		}
		if created && !inMaintenance {
			p.emitNotification("incident.created", inc, mon, nil)
			escalation.StartEscalation(ctx, p.store, mon, inc.ID, p.logger)
		}
	}
}

func HashBody(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:])
}

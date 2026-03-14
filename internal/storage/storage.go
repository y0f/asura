package storage

import (
	"context"
	"time"
)

// Store defines the complete storage interface.
type Store interface {
	// Monitors
	CreateMonitor(ctx context.Context, m *Monitor) error
	GetMonitor(ctx context.Context, id int64) (*Monitor, error)
	ListMonitors(ctx context.Context, f MonitorListFilter, p Pagination) (*PaginatedResult, error)
	UpdateMonitor(ctx context.Context, m *Monitor) error
	DeleteMonitor(ctx context.Context, id int64) error
	SetMonitorEnabled(ctx context.Context, id int64, enabled bool) error
	BulkSetMonitorsEnabled(ctx context.Context, ids []int64, enabled bool) (int64, error)
	BulkDeleteMonitors(ctx context.Context, ids []int64) (int64, error)
	BulkSetMonitorGroup(ctx context.Context, ids []int64, groupID *int64) (int64, error)
	GetAllEnabledMonitors(ctx context.Context) ([]*Monitor, error)

	// Monitor status (runtime state)
	GetMonitorStatus(ctx context.Context, monitorID int64) (*MonitorStatus, error)
	UpsertMonitorStatus(ctx context.Context, s *MonitorStatus) error

	// Check results
	InsertCheckResult(ctx context.Context, r *CheckResult) error
	ListCheckResults(ctx context.Context, monitorID int64, p Pagination) (*PaginatedResult, error)
	GetLatestCheckResult(ctx context.Context, monitorID int64) (*CheckResult, error)
	GetMonitorSparklines(ctx context.Context, monitorIDs []int64, n int) (map[int64][]*SparklinePoint, error)

	// Incidents
	CreateIncident(ctx context.Context, inc *Incident) error
	GetIncident(ctx context.Context, id int64) (*Incident, error)
	ListIncidents(ctx context.Context, monitorID int64, status string, search string, p Pagination) (*PaginatedResult, error)
	UpdateIncident(ctx context.Context, inc *Incident) error
	DeleteIncident(ctx context.Context, id int64) error
	GetOpenIncident(ctx context.Context, monitorID int64) (*Incident, error)

	// Incident events
	InsertIncidentEvent(ctx context.Context, e *IncidentEvent) error
	ListIncidentEvents(ctx context.Context, incidentID int64) ([]*IncidentEvent, error)

	// Notification channels
	CreateNotificationChannel(ctx context.Context, ch *NotificationChannel) error
	GetNotificationChannel(ctx context.Context, id int64) (*NotificationChannel, error)
	ListNotificationChannels(ctx context.Context) ([]*NotificationChannel, error)
	UpdateNotificationChannel(ctx context.Context, ch *NotificationChannel) error
	DeleteNotificationChannel(ctx context.Context, id int64) error

	// Notification history
	InsertNotificationHistory(ctx context.Context, h *NotificationHistory) error
	ListNotificationHistory(ctx context.Context, f NotifHistoryFilter, p Pagination) (*PaginatedResult, error)

	// Maintenance windows
	CreateMaintenanceWindow(ctx context.Context, mw *MaintenanceWindow) error
	GetMaintenanceWindow(ctx context.Context, id int64) (*MaintenanceWindow, error)
	ListMaintenanceWindows(ctx context.Context) ([]*MaintenanceWindow, error)
	UpdateMaintenanceWindow(ctx context.Context, mw *MaintenanceWindow) error
	DeleteMaintenanceWindow(ctx context.Context, id int64) error
	ToggleMaintenanceWindow(ctx context.Context, id int64, active bool) error
	IsMonitorInMaintenance(ctx context.Context, monitorID int64, at time.Time) (bool, error)

	// Content changes
	InsertContentChange(ctx context.Context, c *ContentChange) error
	ListContentChanges(ctx context.Context, monitorID int64, p Pagination) (*PaginatedResult, error)

	// Heartbeats
	CreateHeartbeat(ctx context.Context, h *Heartbeat) error
	GetHeartbeatByToken(ctx context.Context, token string) (*Heartbeat, error)
	GetHeartbeatByMonitorID(ctx context.Context, monitorID int64) (*Heartbeat, error)
	UpdateHeartbeatPing(ctx context.Context, token string) error
	ListExpiredHeartbeats(ctx context.Context) ([]*Heartbeat, error)
	UpdateHeartbeatStatus(ctx context.Context, monitorID int64, status string) error
	DeleteHeartbeat(ctx context.Context, monitorID int64) error

	// Analytics
	GetResponseTimeSeries(ctx context.Context, monitorID int64, from, to time.Time, maxPoints int) ([]*TimeSeriesPoint, error)
	GetUptimePercent(ctx context.Context, monitorID int64, from, to time.Time) (float64, error)
	GetResponseTimePercentiles(ctx context.Context, monitorID int64, from, to time.Time) (p50, p95, p99 float64, err error)
	GetCheckCounts(ctx context.Context, monitorID int64, from, to time.Time) (total, up, down, degraded int64, err error)
	CountMonitorsByStatus(ctx context.Context) (up, down, degraded, paused int64, err error)
	GetLatestResponseTimes(ctx context.Context) (map[int64]int64, error)

	// Monitor notification routing
	GetMonitorNotificationChannelIDs(ctx context.Context, monitorID int64) ([]int64, error)
	SetMonitorNotificationChannels(ctx context.Context, monitorID int64, channelIDs []int64) error

	// Monitor groups
	CreateMonitorGroup(ctx context.Context, g *MonitorGroup) error
	GetMonitorGroup(ctx context.Context, id int64) (*MonitorGroup, error)
	ListMonitorGroups(ctx context.Context) ([]*MonitorGroup, error)
	UpdateMonitorGroup(ctx context.Context, g *MonitorGroup) error
	DeleteMonitorGroup(ctx context.Context, id int64) error

	// Tags
	CreateTag(ctx context.Context, t *Tag) error
	GetTag(ctx context.Context, id int64) (*Tag, error)
	ListTags(ctx context.Context) ([]*Tag, error)
	UpdateTag(ctx context.Context, t *Tag) error
	DeleteTag(ctx context.Context, id int64) error
	SetMonitorTags(ctx context.Context, monitorID int64, tags []MonitorTag) error
	GetMonitorTags(ctx context.Context, monitorID int64) ([]MonitorTag, error)
	GetMonitorTagsBatch(ctx context.Context, monitorIDs []int64) (map[int64][]MonitorTag, error)

	// Audit
	InsertAudit(ctx context.Context, entry *AuditEntry) error
	ListAuditLog(ctx context.Context, f AuditLogFilter, p Pagination) (*PaginatedResult, error)

	// TOTP keys
	CreateTOTPKey(ctx context.Context, key *TOTPKey) error
	GetTOTPKey(ctx context.Context, apiKeyName string) (*TOTPKey, error)
	DeleteTOTPKey(ctx context.Context, apiKeyName string) error

	// Sessions
	CreateSession(ctx context.Context, s *Session) error
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (*Session, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	ExtendSession(ctx context.Context, tokenHash string, newExpiry time.Time) error
	DeleteExpiredSessions(ctx context.Context) (int64, error)
	DeleteSessionsByAPIKeyName(ctx context.Context, apiKeyName string) (int64, error)
	DeleteSessionsExceptKeyNames(ctx context.Context, validNames []string) (int64, error)

	// Request logs
	InsertRequestLogBatch(ctx context.Context, logs []*RequestLog) error
	ListRequestLogs(ctx context.Context, f RequestLogFilter, p Pagination) (*PaginatedResult, error)
	ListTopClientIPs(ctx context.Context, from, to time.Time, limit int) ([]string, error)
	GetRequestLogStats(ctx context.Context, from, to time.Time) (*RequestLogStats, error)
	RollupRequestLogs(ctx context.Context, date string) error
	PurgeOldRequestLogs(ctx context.Context, before time.Time) (int64, error)

	// Status pages
	GetDailyUptime(ctx context.Context, monitorID int64, from, to time.Time) ([]*DailyUptime, error)
	IsMonitorOnStatusPage(ctx context.Context, monitorID int64) (bool, error)
	CreateStatusPage(ctx context.Context, sp *StatusPage) error
	GetStatusPage(ctx context.Context, id int64) (*StatusPage, error)
	GetStatusPageBySlug(ctx context.Context, slug string) (*StatusPage, error)
	ListStatusPages(ctx context.Context) ([]*StatusPage, error)
	UpdateStatusPage(ctx context.Context, sp *StatusPage) error
	DeleteStatusPage(ctx context.Context, id int64) error
	SetStatusPageMonitors(ctx context.Context, pageID int64, monitors []StatusPageMonitor) error
	ListStatusPageMonitors(ctx context.Context, pageID int64) ([]StatusPageMonitor, error)
	ListStatusPageMonitorsWithStatus(ctx context.Context, pageID int64) ([]*Monitor, []StatusPageMonitor, error)

	// Anomaly detection baselines
	UpdateBaseline(ctx context.Context, monitorID int64, avg, stddev float64) error
	GetResponseTimeStats(ctx context.Context, monitorID int64, from time.Time) (avg, stddev float64, count int64, err error)

	// Status page subscribers
	CreateStatusPageSubscriber(ctx context.Context, sub *StatusPageSubscriber) error
	GetSubscriberByToken(ctx context.Context, token string) (*StatusPageSubscriber, error)
	ConfirmSubscriber(ctx context.Context, token string) error
	DeleteSubscriberByToken(ctx context.Context, token string) error
	CountSubscribersByPage(ctx context.Context, pageID int64) (int64, error)
	ListConfirmedSubscribers(ctx context.Context, pageID int64) ([]*StatusPageSubscriber, error)
	DeleteSubscriber(ctx context.Context, id int64) error
	GetStatusPageIDsForMonitor(ctx context.Context, monitorID int64) ([]int64, error)

	// Proxies
	CreateProxy(ctx context.Context, p *Proxy) error
	GetProxy(ctx context.Context, id int64) (*Proxy, error)
	ListProxies(ctx context.Context) ([]*Proxy, error)
	UpdateProxy(ctx context.Context, p *Proxy) error
	DeleteProxy(ctx context.Context, id int64) error

	// Escalation policies
	CreateEscalationPolicy(ctx context.Context, ep *EscalationPolicy) error
	GetEscalationPolicy(ctx context.Context, id int64) (*EscalationPolicy, error)
	ListEscalationPolicies(ctx context.Context) ([]*EscalationPolicy, error)
	UpdateEscalationPolicy(ctx context.Context, ep *EscalationPolicy) error
	DeleteEscalationPolicy(ctx context.Context, id int64) error
	GetEscalationPolicySteps(ctx context.Context, policyID int64) ([]*EscalationPolicyStep, error)
	ReplaceEscalationPolicySteps(ctx context.Context, policyID int64, steps []*EscalationPolicyStep) error

	// Escalation state
	CreateEscalationState(ctx context.Context, state *EscalationState) error
	GetEscalationStateByIncident(ctx context.Context, incidentID int64) (*EscalationState, error)
	ListPendingEscalationStates(ctx context.Context, before time.Time) ([]*EscalationState, error)
	UpdateEscalationState(ctx context.Context, state *EscalationState) error
	DeleteEscalationStateByIncident(ctx context.Context, incidentID int64) error

	// Data rollup
	RollupHourly(ctx context.Context, hour string) error
	RollupDaily(ctx context.Context, day string) error
	PurgeHourlyBefore(ctx context.Context, before time.Time) (int64, error)
	PurgeDailyBefore(ctx context.Context, before time.Time) (int64, error)
	GetTimeSeriesFromHourly(ctx context.Context, monitorID int64, from, to time.Time, maxPoints int) ([]*TimeSeriesPoint, error)
	GetTimeSeriesFromDaily(ctx context.Context, monitorID int64, from, to time.Time) ([]*TimeSeriesPoint, error)

	// Data retention
	PurgeOldData(ctx context.Context, before time.Time) (int64, error)

	// Database maintenance
	Vacuum(ctx context.Context) error
	DBSize() (int64, error)

	// Lifecycle
	Close() error
}

package storage

import (
	"encoding/json"
	"time"

	"github.com/y0f/asura/internal/model"
)

// Monitor and protocol settings are shared with probes without database code.
type (
	Monitor            = model.Monitor
	HTTPSettings       = model.HTTPSettings
	TCPSettings        = model.TCPSettings
	DNSSettings        = model.DNSSettings
	TLSSettings        = model.TLSSettings
	WebSocketSettings  = model.WebSocketSettings
	CommandSettings    = model.CommandSettings
	DockerSettings     = model.DockerSettings
	DomainSettings     = model.DomainSettings
	GRPCSettings       = model.GRPCSettings
	MQTTSettings       = model.MQTTSettings
	SMTPSettings       = model.SMTPSettings
	SSHSettings        = model.SSHSettings
	RedisSettings      = model.RedisSettings
	PostgreSQLSettings = model.PostgreSQLSettings
	UDPSettings        = model.UDPSettings
)

// SparklinePoint holds a single data point for sparkline rendering.
type SparklinePoint struct {
	Status       string
	ResponseTime int64
}

// Observation provenance is assigned by the server when it persists evidence.
// Legacy rows remain unknown because a monitor's current type cannot prove it.
const (
	ObservationUnknown   = "unknown"
	ObservationProbe     = "probe"
	ObservationManual    = "manual"
	ObservationHeartbeat = "heartbeat"
)

// CheckResult stores a probe outcome or recorded state transition.
type CheckResult struct {
	ObservationKind string     `json:"observation_kind"`
	ID              int64      `json:"id"`
	MonitorID       int64      `json:"monitor_id"`
	Status          string     `json:"status"`        // up, down, degraded
	ResponseTime    int64      `json:"response_time"` // milliseconds
	StatusCode      int        `json:"status_code,omitempty"`
	Message         string     `json:"message,omitempty"`
	Headers         string     `json:"headers,omitempty"` // JSON encoded
	Body            string     `json:"body,omitempty"`
	BodyHash        string     `json:"body_hash,omitempty"`
	CertExpiry      *time.Time `json:"cert_expiry,omitempty"`
	CertFingerprint string     `json:"cert_fingerprint,omitempty"`
	DNSRecords      string     `json:"dns_records,omitempty"` // JSON encoded
	AgentID         *int64     `json:"agent_id,omitempty"`
	AgentName       string     `json:"agent_name,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// Agent represents a remote probe that runs checks on behalf of the server.
type Agent struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	Location      string     `json:"location"`
	Token         string     `json:"-"`
	LastHeartbeat *time.Time `json:"last_heartbeat,omitempty"`
	Enabled       bool       `json:"enabled"`
	CreatedAt     time.Time  `json:"created_at"`
}

// OnCallRotation defines a rotating on-call schedule across notification channels.
type OnCallRotation struct {
	ID                int64      `json:"id"`
	Name              string     `json:"name"`
	ChannelIDs        []int64    `json:"channel_ids"`
	Period            string     `json:"period"` // daily, weekly
	CurrentIndex      int        `json:"current_index"`
	PeriodStartedAt   time.Time  `json:"period_started_at"`
	OverrideChannelID *int64     `json:"override_channel_id,omitempty"`
	OverrideUntil     *time.Time `json:"override_until,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// AgentJob is a monitor sent to an agent for checking.
type AgentJob struct {
	ConfigRevision int64           `json:"config_revision"`
	ID             int64           `json:"id"`
	Name           string          `json:"name"`
	Type           string          `json:"type"`
	Target         string          `json:"target"`
	Interval       int             `json:"interval"`
	Timeout        int             `json:"timeout"`
	Settings       json.RawMessage `json:"settings,omitempty"`
}

// Incident tracks a period of downtime or degradation.
type Incident struct {
	// AnnouncementPending survives restarts until the initial notification has
	// been queued. It is internal delivery state, not API-editable incident data.
	AnnouncementPending bool       `json:"-"`
	EscalationPending   bool       `json:"-"`
	ID                  int64      `json:"id"`
	MonitorID           int64      `json:"monitor_id"`
	MonitorName         string     `json:"monitor_name,omitempty"`
	MonitorGroupID      *int64     `json:"-"`
	Status              string     `json:"status"`   // open, acknowledged, resolved
	Severity            string     `json:"severity"` // critical, major, minor, warning
	Cause               string     `json:"cause"`
	StartedAt           time.Time  `json:"started_at"`
	AcknowledgedAt      *time.Time `json:"acknowledged_at,omitempty"`
	AcknowledgedBy      string     `json:"acknowledged_by,omitempty"`
	ResolvedAt          *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy          string     `json:"resolved_by,omitempty"`
}

// IncidentEvent is a timeline entry within an incident.
type IncidentEvent struct {
	ID         int64     `json:"id"`
	IncidentID int64     `json:"incident_id"`
	Type       string    `json:"type"` // created, acknowledged, resolved, check_failed, check_recovered
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"created_at"`
}

// NotificationChannel configures how alerts are delivered.
type NotificationChannel struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Type      string          `json:"type"` // webhook, email, telegram, discord, slack
	Enabled   bool            `json:"enabled"`
	Settings  json.RawMessage `json:"settings"`
	Events    []string        `json:"events"` // incident.created, incident.resolved, etc.
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// MaintenanceWindow defines a period where alerts are suppressed.
type MaintenanceWindow struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	MonitorIDs []int64   `json:"monitor_ids"` // empty = all monitors
	StartTime  time.Time `json:"start_time"`
	EndTime    time.Time `json:"end_time"`
	Recurring  string    `json:"recurring,omitempty"` // "", "manual", "cron", "daily", "weekly", "monthly"
	CronExpr   string    `json:"cron_expr,omitempty"`
	Active     bool      `json:"active"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ContentChange records when a monitored page's content changes.
type ContentChange struct {
	ID        int64     `json:"id"`
	MonitorID int64     `json:"monitor_id"`
	OldHash   string    `json:"old_hash"`
	NewHash   string    `json:"new_hash"`
	Diff      string    `json:"diff"`
	OldBody   string    `json:"-"` // not exposed in API
	NewBody   string    `json:"-"` // not exposed in API
	CreatedAt time.Time `json:"created_at"`
}

// MonitorStatus holds the runtime state of a monitor.
type MonitorStatus struct {
	ObservationKind     string     `json:"observation_kind"`
	MonitorID           int64      `json:"monitor_id"`
	ConfigRevision      int64      `json:"-"`
	Status              string     `json:"status"` // up, down, degraded, pending
	LastCheckAt         *time.Time `json:"last_check_at,omitempty"`
	ConsecFails         int        `json:"consec_fails"`
	ConsecSuccesses     int        `json:"consec_successes"`
	LastBodyHash        string     `json:"-"`
	LastCertFingerprint string     `json:"-"`
	ResponseTime        int64      `json:"response_time,omitempty"`
	BaselineAvg         float64    `json:"baseline_avg,omitempty"`
	BaselineStddev      float64    `json:"baseline_stddev,omitempty"`
	BaselineUpdatedAt   *time.Time `json:"baseline_updated_at,omitempty"`
}

// Pagination contains parameters for list queries.
type Pagination struct {
	Page    int `json:"page"`
	PerPage int `json:"per_page"`
}

// PaginatedResult wraps a list response with metadata.
type PaginatedResult struct {
	Data       any   `json:"data"`
	Total      int64 `json:"total"`
	Page       int   `json:"page"`
	PerPage    int   `json:"per_page"`
	TotalPages int   `json:"total_pages"`
}

// Heartbeat tracks a heartbeat monitor's ping state.
type Heartbeat struct {
	ID         int64      `json:"id"`
	MonitorID  int64      `json:"monitor_id"`
	Token      string     `json:"token"`
	Grace      int        `json:"grace"` // grace period in seconds
	LastPingAt *time.Time `json:"last_ping_at,omitempty"`
	Status     string     `json:"status"` // up, down, pending
}

// AuditEntry logs a mutation in the system.
type AuditEntry struct {
	ID         int64     `json:"id"`
	Action     string    `json:"action"`
	Entity     string    `json:"entity"`
	EntityID   int64     `json:"entity_id"`
	APIKeyName string    `json:"api_key_name"`
	Detail     string    `json:"detail,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// RequestLog records a single HTTP request to the Asura server.
type RequestLog struct {
	ID         int64     `json:"id"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	StatusCode int       `json:"status_code"`
	LatencyMs  int64     `json:"latency_ms"`
	ClientIP   string    `json:"client_ip"`
	UserAgent  string    `json:"user_agent"`
	Referer    string    `json:"referer"`
	MonitorID  *int64    `json:"monitor_id,omitempty"`
	RouteGroup string    `json:"route_group"`
	CreatedAt  time.Time `json:"created_at"`
}

// RequestLogStats holds aggregate request statistics.
type RequestLogStats struct {
	TotalRequests  int64       `json:"total_requests"`
	UniqueVisitors int64       `json:"unique_visitors"`
	AvgLatencyMs   int64       `json:"avg_latency_ms"`
	TopPaths       []PathCount `json:"top_paths"`
	TopReferers    []PathCount `json:"top_referers"`
}

// PathCount pairs a path or referer with its request count.
type PathCount struct {
	Path  string `json:"path"`
	Count int64  `json:"count"`
}

// Tag represents a reusable tag with a name and color.
type Tag struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"created_at"`
}

// MonitorTag links a tag to a monitor with an optional per-monitor value.
type MonitorTag = model.MonitorTag

// MonitorGroup organizes monitors into logical groups.
type MonitorGroup struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	SortOrder int       `json:"sort_order"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MonitorListFilter holds filter parameters for listing monitors.
type MonitorListFilter struct {
	Access  []MonitorAccess // every supplied filter is required; nil is internal/unrestricted
	Type    string
	Search  string
	GroupID *int64
	TagID   *int64
	Status  string // up, down, degraded, paused
	Sort    string // name, status, last_check, response_time
}

type IncidentListFilter struct {
	MonitorID             int64
	Status                string
	Search                string
	Access                []MonitorAccess
	MonitorReadAccess     []MonitorAccess
	DiagnosticsReadAccess []MonitorAccess
}

// AuditLogFilter holds filter parameters for listing audit log entries.
type AuditLogFilter struct {
	Action     string
	Entity     string
	APIKeyName string
	From       time.Time
	To         time.Time
}

// NotificationHistory records a single notification delivery attempt.
type NotificationHistory struct {
	ID             int64     `json:"id"`
	ChannelID      int64     `json:"channel_id"`
	ChannelName    string    `json:"channel_name"`
	ChannelType    string    `json:"channel_type"`
	MonitorID      *int64    `json:"monitor_id,omitempty"`
	MonitorName    string    `json:"monitor_name,omitempty"`
	MonitorGroupID *int64    `json:"-"`
	IncidentID     *int64    `json:"incident_id,omitempty"`
	EventType      string    `json:"event_type"`
	Status         string    `json:"status"` // "sent" or "failed"
	Error          string    `json:"error,omitempty"`
	SentAt         time.Time `json:"sent_at"`
}

// NotifHistoryFilter holds filter parameters for notification history queries.
type NotifHistoryFilter struct {
	Access    []MonitorAccess
	ChannelID int64
	Status    string // "sent", "failed", or ""
	EventType string
}

// RequestLogFilter holds filter parameters for listing request logs.
type RequestLogFilter struct {
	Access     []MonitorAccess
	Method     string
	Path       string
	StatusCode int
	RouteGroup string
	ClientIP   string
	MonitorID  *int64
	From       time.Time
	To         time.Time
}

// TimeSeriesPoint is a single data point for response time charts.
type TimeSeriesPoint struct {
	Timestamp    int64  `json:"ts"`
	ResponseTime int64  `json:"rt"`
	Status       string `json:"s"`
}

// StatusPage represents a public status page with its own slug and monitor set.
type StatusPage struct {
	ID               int64     `json:"id"`
	Slug             string    `json:"slug"`
	Title            string    `json:"title"`
	Description      string    `json:"description"`
	CustomCSS        string    `json:"custom_css"`
	ShowIncidents    bool      `json:"show_incidents"`
	Enabled          bool      `json:"enabled"`
	APIEnabled       bool      `json:"api_enabled"`
	SortOrder        int       `json:"sort_order"`
	LogoURL          string    `json:"logo_url"`
	FaviconURL       string    `json:"favicon_url"`
	CustomHeaderHTML string    `json:"custom_header_html"`
	PasswordHash     string    `json:"-"`
	PasswordEnabled  bool      `json:"password_enabled"`
	AnalyticsScript  string    `json:"analytics_script"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`

	// Transient fields
	MonitorCount int `json:"monitor_count,omitempty"`
}

// StatusPageMonitor links a monitor to a status page with display options.
type StatusPageMonitor struct {
	PageID    int64  `json:"page_id"`
	MonitorID int64  `json:"monitor_id"`
	SortOrder int    `json:"sort_order"`
	GroupName string `json:"group_name"`
}

// StatusPageSubscriber represents a subscriber to a status page.
type StatusPageSubscriber struct {
	ID           int64     `json:"id"`
	StatusPageID int64     `json:"status_page_id"`
	Type         string    `json:"type"` // "email" or "webhook"
	Email        string    `json:"email,omitempty"`
	WebhookURL   string    `json:"webhook_url,omitempty"`
	Confirmed    bool      `json:"confirmed"`
	Token        string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// DailyUptime holds uptime statistics for a single day.
type DailyUptime struct {
	Date        string  `json:"date"`
	TotalChecks int64   `json:"total_checks"`
	UpChecks    int64   `json:"up_checks"`
	DownChecks  int64   `json:"down_checks"`
	UptimePct   float64 `json:"uptime_pct"`
}

// TOTPKey stores a TOTP secret for an API key.
type TOTPKey struct {
	ID         int64     `json:"id"`
	APIKeyName string    `json:"api_key_name"`
	Secret     string    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
}

// Proxy represents a configured proxy server.
type Proxy struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Protocol  string    `json:"protocol"` // http, socks5
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	AuthUser  string    `json:"auth_user,omitempty"`
	AuthPass  string    `json:"-"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EscalationPolicy defines a time-based notification escalation chain.
type EscalationPolicy struct {
	ID          int64                   `json:"id"`
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Enabled     bool                    `json:"enabled"`
	Repeat      bool                    `json:"repeat"`
	Steps       []*EscalationPolicyStep `json:"steps,omitempty"`
	CreatedAt   time.Time               `json:"created_at"`
	UpdatedAt   time.Time               `json:"updated_at"`
}

// EscalationPolicyStep defines one step in an escalation chain.
type EscalationPolicyStep struct {
	ID                     int64   `json:"id,omitempty"`
	PolicyID               int64   `json:"policy_id,omitempty"`
	StepOrder              int     `json:"step_order"`
	DelayMinutes           int     `json:"delay_minutes"`
	NotificationChannelIDs []int64 `json:"notification_channel_ids"`
}

// EscalationState tracks the current escalation progress for an incident.
type EscalationState struct {
	ID          int64     `json:"id"`
	IncidentID  int64     `json:"incident_id"`
	PolicyID    int64     `json:"policy_id"`
	CurrentStep int       `json:"current_step"`
	NextFireAt  time.Time `json:"next_fire_at"`
	StartedAt   time.Time `json:"started_at"`
}

// Session represents a server-side web UI session.
type Session struct {
	ID         int64     `json:"id"`
	TokenHash  string    `json:"-"`
	APIKeyName string    `json:"api_key_name"`
	KeyHash    string    `json:"-"`
	IPAddress  string    `json:"ip_address"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

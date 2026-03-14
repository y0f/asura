package storage

import (
	"encoding/json"
	"time"
)

// Monitor represents a monitored endpoint.
type Monitor struct {
	ID                 int64           `json:"id"`
	Name               string          `json:"name"`
	Description        string          `json:"description,omitempty"`
	Type               string          `json:"type"` // http, tcp, dns, icmp, tls, websocket, command
	Target             string          `json:"target"`
	Interval           int             `json:"interval"` // seconds
	Timeout            int             `json:"timeout"`  // seconds
	Enabled            bool            `json:"enabled"`
	Tags               []string        `json:"tags"`
	Settings           json.RawMessage `json:"settings,omitempty"`
	Assertions         json.RawMessage `json:"assertions,omitempty"`
	TrackChanges       bool            `json:"track_changes"`
	FailureThreshold   int             `json:"failure_threshold"`
	SuccessThreshold   int             `json:"success_threshold"`
	UpsideDown         bool            `json:"upside_down"`
	ResendInterval     int             `json:"resend_interval"`
	SLATarget          float64         `json:"sla_target"`
	AnomalySensitivity string          `json:"anomaly_sensitivity,omitempty"` // off, low, medium, high
	GroupID            *int64          `json:"group_id,omitempty"`
	ProxyID            *int64          `json:"proxy_id,omitempty"`
	EscalationPolicyID *int64          `json:"escalation_policy_id,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`

	// Transient fields (not stored in monitors table)
	NotificationChannelIDs []int64      `json:"notification_channel_ids,omitempty"`
	MonitorTags            []MonitorTag `json:"monitor_tags,omitempty"`
	ProxyURL               string       `json:"-"` // resolved at check time

	// Computed fields (not stored directly)
	Status          string     `json:"status,omitempty"`
	LastCheckAt     *time.Time `json:"last_check_at,omitempty"`
	ConsecFails     int        `json:"consec_fails,omitempty"`
	ConsecSuccesses int        `json:"consec_successes,omitempty"`
}

// HTTPSettings holds configuration specific to HTTP checks.
type HTTPSettings struct {
	Method             string            `json:"method,omitempty"`
	Headers            map[string]string `json:"headers,omitempty"`
	Body               string            `json:"body,omitempty"`
	BodyEncoding       string            `json:"body_encoding,omitempty"` // json, xml, form, raw
	FollowRedirects    *bool             `json:"follow_redirects,omitempty"`
	MaxRedirects       int               `json:"max_redirects,omitempty"`
	SkipTLSVerify      bool              `json:"skip_tls_verify,omitempty"`
	CacheBuster        bool              `json:"cache_buster,omitempty"`
	AuthMethod         string            `json:"auth_method,omitempty"` // none, basic, bearer, oauth2
	BasicAuthUser      string            `json:"basic_auth_user,omitempty"`
	BasicAuthPass      string            `json:"basic_auth_pass,omitempty"`
	BearerToken        string            `json:"bearer_token,omitempty"`
	OAuth2TokenURL     string            `json:"oauth2_token_url,omitempty"`
	OAuth2ClientID     string            `json:"oauth2_client_id,omitempty"`
	OAuth2ClientSecret string            `json:"oauth2_client_secret,omitempty"`
	OAuth2Scopes       string            `json:"oauth2_scopes,omitempty"`
	OAuth2Audience     string            `json:"oauth2_audience,omitempty"`
	MTLSEnabled        bool              `json:"mtls_enabled,omitempty"`
	MTLSClientCert     string            `json:"mtls_client_cert,omitempty"`
	MTLSClientKey      string            `json:"mtls_client_key,omitempty"`
	MTLSCACert         string            `json:"mtls_ca_cert,omitempty"`
	ExpectedStatus     int               `json:"expected_status,omitempty"`
}

// TCPSettings holds TCP check configuration.
type TCPSettings struct {
	SendData   string `json:"send_data,omitempty"`
	ExpectData string `json:"expect_data,omitempty"`
}

// DNSSettings holds DNS check configuration.
type DNSSettings struct {
	RecordType string `json:"record_type"` // A, AAAA, CNAME, MX, TXT, NS, SOA
	Server     string `json:"server,omitempty"`
}

// TLSSettings holds TLS check configuration.
type TLSSettings struct {
	WarnDaysBefore int `json:"warn_days_before,omitempty"` // cert expiry warning threshold
}

// WebSocketSettings holds WebSocket check configuration.
type WebSocketSettings struct {
	Headers     map[string]string `json:"headers,omitempty"`
	SendMessage string            `json:"send_message,omitempty"`
	ExpectReply string            `json:"expect_reply,omitempty"`
}

// CommandSettings holds command check configuration.
type CommandSettings struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// DockerSettings holds Docker container check configuration.
type DockerSettings struct {
	ContainerName string `json:"container_name"`
	SocketPath    string `json:"socket_path,omitempty"`
	CheckHealth   bool   `json:"check_health,omitempty"`
}

// DomainSettings holds domain expiry (WHOIS) check configuration.
type DomainSettings struct {
	WarnDaysBefore int `json:"warn_days_before,omitempty"`
}

// GRPCSettings holds gRPC health check configuration.
type GRPCSettings struct {
	ServiceName   string `json:"service_name,omitempty"`
	UseTLS        bool   `json:"use_tls,omitempty"`
	SkipTLSVerify bool   `json:"skip_tls_verify,omitempty"`
}

// MQTTSettings holds MQTT connection check configuration.
type MQTTSettings struct {
	ClientID      string `json:"client_id,omitempty"`
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	Topic         string `json:"topic,omitempty"`
	ExpectMessage string `json:"expect_message,omitempty"`
	UseTLS        bool   `json:"use_tls,omitempty"`
}

// SMTPSettings holds SMTP check configuration.
type SMTPSettings struct {
	Port         int    `json:"port,omitempty"`
	STARTTLS     bool   `json:"starttls,omitempty"`
	ExpectBanner string `json:"expect_banner,omitempty"`
}

// SSHSettings holds SSH check configuration.
type SSHSettings struct {
	ExpectedFingerprint string `json:"expected_fingerprint,omitempty"`
}

// SparklinePoint holds a single data point for sparkline rendering.
type SparklinePoint struct {
	Status       string
	ResponseTime int64
}

// CheckResult stores the outcome of a single check execution.
type CheckResult struct {
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
	CreatedAt       time.Time  `json:"created_at"`
}

// Incident tracks a period of downtime or degradation.
type Incident struct {
	ID             int64      `json:"id"`
	MonitorID      int64      `json:"monitor_id"`
	MonitorName    string     `json:"monitor_name,omitempty"`
	Status         string     `json:"status"` // open, acknowledged, resolved
	Cause          string     `json:"cause"`
	StartedAt      time.Time  `json:"started_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	AcknowledgedBy string     `json:"acknowledged_by,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy     string     `json:"resolved_by,omitempty"`
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
	MonitorID           int64      `json:"monitor_id"`
	Status              string     `json:"status"` // up, down, degraded, pending
	LastCheckAt         *time.Time `json:"last_check_at,omitempty"`
	ConsecFails         int        `json:"consec_fails"`
	ConsecSuccesses     int        `json:"consec_successes"`
	LastBodyHash        string     `json:"-"`
	LastCertFingerprint string     `json:"-"`
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
type MonitorTag struct {
	TagID int64  `json:"tag_id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Value string `json:"value"`
}

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
	Type    string
	Search  string
	GroupID *int64
	TagID   *int64
	Status  string // up, down, degraded, paused
	Sort    string // name, status, last_check, response_time
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
	ID          int64     `json:"id"`
	ChannelID   int64     `json:"channel_id"`
	ChannelName string    `json:"channel_name"`
	ChannelType string    `json:"channel_type"`
	MonitorID   *int64    `json:"monitor_id,omitempty"`
	MonitorName string    `json:"monitor_name,omitempty"`
	IncidentID  *int64    `json:"incident_id,omitempty"`
	EventType   string    `json:"event_type"`
	Status      string    `json:"status"` // "sent" or "failed"
	Error       string    `json:"error,omitempty"`
	SentAt      time.Time `json:"sent_at"`
}

// NotifHistoryFilter holds filter parameters for notification history queries.
type NotifHistoryFilter struct {
	ChannelID int64
	Status    string // "sent", "failed", or ""
	EventType string
}

// RequestLogFilter holds filter parameters for listing request logs.
type RequestLogFilter struct {
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

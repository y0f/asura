// Package model contains monitor data shared by the server and remote probes.
package model

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
	AgentEnabled       bool            `json:"agent_enabled"`
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
	// AgentIDs are the agents this monitor is assigned to; empty means every agent.
	AgentIDs []int64 `json:"agent_ids,omitempty"`
	ProxyURL string  `json:"-"` // resolved at check time

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

// RedisSettings holds Redis check configuration.
type RedisSettings struct {
	Password string `json:"password,omitempty"`
}

// PostgreSQLSettings holds PostgreSQL check configuration.
type PostgreSQLSettings struct {
	Username string `json:"username,omitempty"`
	Database string `json:"database,omitempty"`
}

// UDPSettings holds UDP check configuration.
type UDPSettings struct {
	SendData   string `json:"send_data,omitempty"`
	ExpectData string `json:"expect_data,omitempty"`
}

// MonitorTag links a tag to a monitor with an optional per-monitor value.
type MonitorTag struct {
	TagID int64  `json:"tag_id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Value string `json:"value"`
}

// MultiStepHTTPSettings holds configuration for multi-step HTTP checks.
type MultiStepHTTPSettings struct {
	Steps []HTTPStep `json:"steps"`
}

// HTTPStep defines a single request in a multi-step sequence.
type HTTPStep struct {
	Name           string            `json:"name"`
	URL            string            `json:"url"`
	Method         string            `json:"method"`
	Headers        map[string]string `json:"headers,omitempty"`
	Body           string            `json:"body,omitempty"`
	ExpectedStatus int               `json:"expected_status,omitempty"`
	ExtractVar     string            `json:"extract_var,omitempty"`   // variable name to set
	ExtractRegex   string            `json:"extract_regex,omitempty"` // regex with capture group
	ExtractJSON    string            `json:"extract_json,omitempty"`  // simple dot-path like "data.token"
}

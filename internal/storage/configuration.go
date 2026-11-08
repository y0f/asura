package storage

// ConfigurationSnapshot contains portable database configuration only. IDs are
// local to this snapshot and must be remapped on import. It deliberately omits
// history, subscribers, sessions, auth/RBAC YAML and agent/heartbeat credentials.
type ConfigurationSnapshot struct {
	Monitors           []*Monitor
	Groups             []*MonitorGroup
	Tags               []*Tag
	Proxies            []*Proxy
	Channels           []*NotificationChannel
	EscalationPolicies []*EscalationPolicy
	Rotations          []*OnCallRotation
	Maintenance        []*MaintenanceWindow
	StatusPages        []*StatusPage
	StatusPageMonitors []StatusPageMonitor
	Agents             []ConfigurationAgent
	HeartbeatGrace     map[int64]int
}

// ConfigurationAgent describes a destination-local enrollment. Credentials and
// runtime addresses never form part of a portable configuration file.
type ConfigurationAgent struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type ConfigurationImportOptions struct {
	Mode string
	// AgentIDs maps source-local references to explicitly selected destination
	// enrollments. Otherwise a unique existing name must match exactly.
	AgentIDs map[int64]int64
	// Validate performs typed validation and may normalize the private snapshot,
	// with no I/O, while the writer transaction is held. Catalogs and references
	// are checked afterwards. This keeps validation independent of SQL backends.
	Validate func(*ConfigurationSnapshot) error
}

type ConfigurationImportResult struct {
	Groups      int                 `json:"groups_created"`
	Tags        int                 `json:"tags_created"`
	Proxies     int                 `json:"proxies_created"`
	Channels    int                 `json:"channels_created"`
	Monitors    int                 `json:"monitors_created"`
	Maintenance int                 `json:"maintenance_created"`
	StatusPages int                 `json:"status_pages_created"`
	Escalations int                 `json:"escalations_created"`
	Rotations   int                 `json:"rotations_created"`
	Skipped     int                 `json:"skipped"`
	Errors      int                 `json:"errors"` // always zero on success; failures roll back
	Heartbeats  []ImportedHeartbeat `json:"heartbeats,omitempty"`
}

// ImportedHeartbeat is returned once after commit to enroll a new pinger. It
// must never be logged, persisted in exports or put in redirect/query strings.
type ImportedHeartbeat struct {
	MonitorID   int64  `json:"monitor_id"`
	MonitorName string `json:"monitor_name"`
	Token       string `json:"token"`
}

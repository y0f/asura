package api

import (
	"encoding/json"
	"time"

	"github.com/y0f/asura/v3/internal/storage"
)

// ConfigurationFile is the portable v2 format. Every ID refers only to an
// entity in this file. Runtime state, auth/RBAC YAML and enrollment credentials
// are deliberately absent. Redacted files are for sharing, never restoration.
type ConfigurationFile struct {
	Version              int                          `json:"version"`
	ExportedAt           time.Time                    `json:"exported_at"`
	Redacted             bool                         `json:"redacted"`
	Monitors             []ConfigurationMonitor       `json:"monitors"`
	MonitorGroups        []ConfigurationGroup         `json:"monitor_groups"`
	Tags                 []ConfigurationTag           `json:"tags"`
	Proxies              []ConfigurationProxy         `json:"proxies"`
	NotificationChannels []ConfigurationChannel       `json:"notification_channels"`
	EscalationPolicies   []ConfigurationEscalation    `json:"escalation_policies"`
	OnCallRotations      []ConfigurationRotation      `json:"on_call_rotations"`
	MaintenanceWindows   []ConfigurationMaintenance   `json:"maintenance_windows"`
	StatusPages          []ConfigurationStatusPage    `json:"status_pages"`
	Agents               []storage.ConfigurationAgent `json:"agents"`
}

type ConfigurationMonitor struct {
	ID                     int64                     `json:"id"`
	Name                   string                    `json:"name"`
	Description            string                    `json:"description,omitempty"`
	Type                   string                    `json:"type"`
	Target                 string                    `json:"target"`
	Interval               int                       `json:"interval"`
	Timeout                int                       `json:"timeout"`
	Enabled                bool                      `json:"enabled"`
	LegacyTags             []string                  `json:"legacy_tags,omitempty"`
	Settings               json.RawMessage           `json:"settings,omitempty"`
	Assertions             json.RawMessage           `json:"assertions,omitempty"`
	TrackChanges           bool                      `json:"track_changes"`
	FailureThreshold       int                       `json:"failure_threshold"`
	SuccessThreshold       int                       `json:"success_threshold"`
	UpsideDown             bool                      `json:"upside_down"`
	ResendInterval         int                       `json:"resend_interval"`
	SLATarget              float64                   `json:"sla_target"`
	AnomalySensitivity     string                    `json:"anomaly_sensitivity,omitempty"`
	GroupID                *int64                    `json:"group_id,omitempty"`
	ProxyID                *int64                    `json:"proxy_id,omitempty"`
	EscalationPolicyID     *int64                    `json:"escalation_policy_id,omitempty"`
	NotificationChannelIDs []int64                   `json:"notification_channel_ids,omitempty"`
	Tags                   []ConfigurationMonitorTag `json:"tags,omitempty"`
	AgentEnabled           bool                      `json:"agent_enabled"`
	AgentIDs               []int64                   `json:"agent_ids,omitempty"`
	HeartbeatGrace         *int                      `json:"heartbeat_grace,omitempty"`
}

type ConfigurationGroup struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
}

type ConfigurationTag struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type ConfigurationMonitorTag struct {
	TagID int64  `json:"tag_id"`
	Value string `json:"value"`
}

type ConfigurationProxy struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	AuthUser string `json:"auth_user,omitempty"`
	AuthPass string `json:"auth_pass,omitempty"`
	Enabled  bool   `json:"enabled"`
}

type ConfigurationChannel struct {
	ID       int64           `json:"id"`
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Enabled  bool            `json:"enabled"`
	Settings json.RawMessage `json:"settings"`
	Events   []string        `json:"events"`
}

type ConfigurationEscalation struct {
	ID          int64                         `json:"id"`
	Name        string                        `json:"name"`
	Description string                        `json:"description"`
	Enabled     bool                          `json:"enabled"`
	Repeat      bool                          `json:"repeat"`
	Steps       []ConfigurationEscalationStep `json:"steps"`
}

type ConfigurationEscalationStep struct {
	StepOrder              int     `json:"step_order"`
	DelayMinutes           int     `json:"delay_minutes"`
	NotificationChannelIDs []int64 `json:"notification_channel_ids"`
}

type ConfigurationRotation struct {
	ID                int64      `json:"id"`
	Name              string     `json:"name"`
	ChannelIDs        []int64    `json:"channel_ids"`
	Period            string     `json:"period"`
	CurrentIndex      int        `json:"current_index"`
	PeriodStartedAt   time.Time  `json:"period_started_at"`
	OverrideChannelID *int64     `json:"override_channel_id,omitempty"`
	OverrideUntil     *time.Time `json:"override_until,omitempty"`
}

type ConfigurationMaintenance struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	MonitorIDs []int64   `json:"monitor_ids"`
	StartTime  time.Time `json:"start_time"`
	EndTime    time.Time `json:"end_time"`
	Recurring  string    `json:"recurring,omitempty"`
	CronExpr   string    `json:"cron_expr,omitempty"`
	Active     bool      `json:"active"`
}

type ConfigurationStatusPage struct {
	ID               int64                      `json:"id"`
	Slug             string                     `json:"slug"`
	Title            string                     `json:"title"`
	Description      string                     `json:"description"`
	CustomCSS        string                     `json:"custom_css"`
	ShowIncidents    bool                       `json:"show_incidents"`
	Enabled          bool                       `json:"enabled"`
	APIEnabled       bool                       `json:"api_enabled"`
	SortOrder        int                        `json:"sort_order"`
	LogoURL          string                     `json:"logo_url"`
	FaviconURL       string                     `json:"favicon_url"`
	CustomHeaderHTML string                     `json:"custom_header_html"`
	PasswordHash     string                     `json:"password_hash,omitempty"`
	AnalyticsScript  string                     `json:"analytics_script"`
	Monitors         []ConfigurationPageMonitor `json:"monitors"`
}

type ConfigurationPageMonitor struct {
	MonitorID int64  `json:"monitor_id"`
	SortOrder int    `json:"sort_order"`
	GroupName string `json:"group_name"`
}

func configurationFile(c *storage.ConfigurationSnapshot, hideSecrets bool) *ConfigurationFile {
	f := &ConfigurationFile{Version: 2, ExportedAt: time.Now().UTC(), Redacted: hideSecrets, Agents: c.Agents}
	for _, m := range c.Monitors {
		out := ConfigurationMonitor{ID: m.ID, Name: m.Name, Description: m.Description, Type: m.Type, Target: m.Target, Interval: m.Interval, Timeout: m.Timeout, Enabled: m.Enabled, LegacyTags: m.Tags, Settings: m.Settings, Assertions: m.Assertions, TrackChanges: m.TrackChanges, FailureThreshold: m.FailureThreshold, SuccessThreshold: m.SuccessThreshold, UpsideDown: m.UpsideDown, ResendInterval: m.ResendInterval, SLATarget: m.SLATarget, AnomalySensitivity: m.AnomalySensitivity, GroupID: m.GroupID, ProxyID: m.ProxyID, EscalationPolicyID: m.EscalationPolicyID, NotificationChannelIDs: m.NotificationChannelIDs, AgentEnabled: m.AgentEnabled, AgentIDs: m.AgentIDs}
		if hideSecrets {
			out.Target, out.Settings, out.Assertions = "", json.RawMessage(`{}`), json.RawMessage(`[]`)
		}
		for _, tag := range m.MonitorTags {
			out.Tags = append(out.Tags, ConfigurationMonitorTag{TagID: tag.TagID, Value: tag.Value})
		}
		if grace, ok := c.HeartbeatGrace[m.ID]; ok {
			out.HeartbeatGrace = &grace
		}
		f.Monitors = append(f.Monitors, out)
	}
	for _, g := range c.Groups {
		f.MonitorGroups = append(f.MonitorGroups, ConfigurationGroup{ID: g.ID, Name: g.Name, SortOrder: g.SortOrder})
	}
	for _, t := range c.Tags {
		f.Tags = append(f.Tags, ConfigurationTag{ID: t.ID, Name: t.Name, Color: t.Color})
	}
	for _, p := range c.Proxies {
		out := ConfigurationProxy{ID: p.ID, Name: p.Name, Protocol: p.Protocol, Host: p.Host, Port: p.Port, AuthUser: p.AuthUser, AuthPass: p.AuthPass, Enabled: p.Enabled}
		if hideSecrets {
			out.AuthUser, out.AuthPass = "", ""
		}
		f.Proxies = append(f.Proxies, out)
	}
	for _, ch := range c.Channels {
		out := ConfigurationChannel{ID: ch.ID, Name: ch.Name, Type: ch.Type, Enabled: ch.Enabled, Settings: ch.Settings, Events: ch.Events}
		if hideSecrets {
			out.Settings = json.RawMessage(`{}`)
		}
		f.NotificationChannels = append(f.NotificationChannels, out)
	}
	for _, p := range c.EscalationPolicies {
		out := ConfigurationEscalation{ID: p.ID, Name: p.Name, Description: p.Description, Enabled: p.Enabled, Repeat: p.Repeat}
		for _, step := range p.Steps {
			out.Steps = append(out.Steps, ConfigurationEscalationStep{StepOrder: step.StepOrder, DelayMinutes: step.DelayMinutes, NotificationChannelIDs: step.NotificationChannelIDs})
		}
		f.EscalationPolicies = append(f.EscalationPolicies, out)
	}
	for _, r := range c.Rotations {
		f.OnCallRotations = append(f.OnCallRotations, ConfigurationRotation{ID: r.ID, Name: r.Name, ChannelIDs: r.ChannelIDs, Period: r.Period, CurrentIndex: r.CurrentIndex, PeriodStartedAt: r.PeriodStartedAt, OverrideChannelID: r.OverrideChannelID, OverrideUntil: r.OverrideUntil})
	}
	for _, m := range c.Maintenance {
		f.MaintenanceWindows = append(f.MaintenanceWindows, ConfigurationMaintenance{ID: m.ID, Name: m.Name, MonitorIDs: m.MonitorIDs, StartTime: m.StartTime, EndTime: m.EndTime, Recurring: m.Recurring, CronExpr: m.CronExpr, Active: m.Active})
	}
	pageMonitors := make(map[int64][]ConfigurationPageMonitor)
	for _, m := range c.StatusPageMonitors {
		pageMonitors[m.PageID] = append(pageMonitors[m.PageID], ConfigurationPageMonitor{MonitorID: m.MonitorID, SortOrder: m.SortOrder, GroupName: m.GroupName})
	}
	for _, p := range c.StatusPages {
		out := ConfigurationStatusPage{ID: p.ID, Slug: p.Slug, Title: p.Title, Description: p.Description, CustomCSS: p.CustomCSS, ShowIncidents: p.ShowIncidents, Enabled: p.Enabled, APIEnabled: p.APIEnabled, SortOrder: p.SortOrder, LogoURL: p.LogoURL, FaviconURL: p.FaviconURL, CustomHeaderHTML: p.CustomHeaderHTML, PasswordHash: p.PasswordHash, AnalyticsScript: p.AnalyticsScript, Monitors: pageMonitors[p.ID]}
		if hideSecrets {
			out.PasswordHash = ""
		}
		f.StatusPages = append(f.StatusPages, out)
	}
	return f
}

func (f *ConfigurationFile) snapshot() *storage.ConfigurationSnapshot {
	c := &storage.ConfigurationSnapshot{Agents: f.Agents, HeartbeatGrace: make(map[int64]int)}
	for _, m := range f.Monitors {
		out := &storage.Monitor{ID: m.ID, Name: m.Name, Description: m.Description, Type: m.Type, Target: m.Target, Interval: m.Interval, Timeout: m.Timeout, Enabled: m.Enabled, Tags: m.LegacyTags, Settings: m.Settings, Assertions: m.Assertions, TrackChanges: m.TrackChanges, FailureThreshold: m.FailureThreshold, SuccessThreshold: m.SuccessThreshold, UpsideDown: m.UpsideDown, ResendInterval: m.ResendInterval, SLATarget: m.SLATarget, AnomalySensitivity: m.AnomalySensitivity, GroupID: m.GroupID, ProxyID: m.ProxyID, EscalationPolicyID: m.EscalationPolicyID, NotificationChannelIDs: m.NotificationChannelIDs, AgentEnabled: m.AgentEnabled, AgentIDs: m.AgentIDs}
		for _, tag := range m.Tags {
			out.MonitorTags = append(out.MonitorTags, storage.MonitorTag{TagID: tag.TagID, Value: tag.Value})
		}
		if m.HeartbeatGrace != nil {
			c.HeartbeatGrace[m.ID] = *m.HeartbeatGrace
		}
		c.Monitors = append(c.Monitors, out)
	}
	for _, g := range f.MonitorGroups {
		c.Groups = append(c.Groups, &storage.MonitorGroup{ID: g.ID, Name: g.Name, SortOrder: g.SortOrder})
	}
	for _, t := range f.Tags {
		c.Tags = append(c.Tags, &storage.Tag{ID: t.ID, Name: t.Name, Color: t.Color})
	}
	for _, p := range f.Proxies {
		c.Proxies = append(c.Proxies, &storage.Proxy{ID: p.ID, Name: p.Name, Protocol: p.Protocol, Host: p.Host, Port: p.Port, AuthUser: p.AuthUser, AuthPass: p.AuthPass, Enabled: p.Enabled})
	}
	for _, ch := range f.NotificationChannels {
		c.Channels = append(c.Channels, &storage.NotificationChannel{ID: ch.ID, Name: ch.Name, Type: ch.Type, Enabled: ch.Enabled, Settings: ch.Settings, Events: ch.Events})
	}
	for _, p := range f.EscalationPolicies {
		out := &storage.EscalationPolicy{ID: p.ID, Name: p.Name, Description: p.Description, Enabled: p.Enabled, Repeat: p.Repeat}
		for _, step := range p.Steps {
			out.Steps = append(out.Steps, &storage.EscalationPolicyStep{StepOrder: step.StepOrder, DelayMinutes: step.DelayMinutes, NotificationChannelIDs: step.NotificationChannelIDs})
		}
		c.EscalationPolicies = append(c.EscalationPolicies, out)
	}
	for _, r := range f.OnCallRotations {
		c.Rotations = append(c.Rotations, &storage.OnCallRotation{ID: r.ID, Name: r.Name, ChannelIDs: r.ChannelIDs, Period: r.Period, CurrentIndex: r.CurrentIndex, PeriodStartedAt: r.PeriodStartedAt, OverrideChannelID: r.OverrideChannelID, OverrideUntil: r.OverrideUntil})
	}
	for _, m := range f.MaintenanceWindows {
		c.Maintenance = append(c.Maintenance, &storage.MaintenanceWindow{ID: m.ID, Name: m.Name, MonitorIDs: m.MonitorIDs, StartTime: m.StartTime, EndTime: m.EndTime, Recurring: m.Recurring, CronExpr: m.CronExpr, Active: m.Active})
	}
	for _, p := range f.StatusPages {
		c.StatusPages = append(c.StatusPages, &storage.StatusPage{ID: p.ID, Slug: p.Slug, Title: p.Title, Description: p.Description, CustomCSS: p.CustomCSS, ShowIncidents: p.ShowIncidents, Enabled: p.Enabled, APIEnabled: p.APIEnabled, SortOrder: p.SortOrder, LogoURL: p.LogoURL, FaviconURL: p.FaviconURL, CustomHeaderHTML: p.CustomHeaderHTML, PasswordHash: p.PasswordHash, PasswordEnabled: p.PasswordHash != "", AnalyticsScript: p.AnalyticsScript})
		for _, m := range p.Monitors {
			c.StatusPageMonitors = append(c.StatusPageMonitors, storage.StatusPageMonitor{PageID: p.ID, MonitorID: m.MonitorID, SortOrder: m.SortOrder, GroupName: m.GroupName})
		}
	}
	return c
}

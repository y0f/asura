package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/internal/storage"
)

func transferMonitor() *storage.Monitor {
	return &storage.Monitor{ID: 1, Name: "web", Type: "http", Target: "https://example.test/secret", Interval: 60, Timeout: 10, Enabled: true, Settings: json.RawMessage(`{"method":"GET","headers":{"X-Token":"custom-secret"},"body":"body-secret","basic_auth_pass":"auth-secret"}`), Assertions: json.RawMessage(`{"groups":[{"conditions":[{"type":"body_contains","value":"assertion-secret"}]}]}`), FailureThreshold: 1, SuccessThreshold: 1}
}

func TestConfigurationFormatPreservesPortableFieldsAndOmitsRuntime(t *testing.T) {
	m := transferMonitor()
	group, proxy, policy := int64(3), int64(4), int64(6)
	m.GroupID, m.ProxyID, m.EscalationPolicyID = &group, &proxy, &policy
	m.Tags = []string{"legacy"}
	m.MonitorTags = []storage.MonitorTag{{TagID: 9, Value: "platform"}}
	m.NotificationChannelIDs, m.AgentIDs = []int64{5}, []int64{8}
	m.TrackChanges, m.UpsideDown, m.SLATarget, m.AnomalySensitivity = true, true, 99.9, "high"
	anchor := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	end := anchor.Add(time.Hour)
	c := &storage.ConfigurationSnapshot{
		Monitors:           []*storage.Monitor{m, {ID: 2, Name: "job", Type: "heartbeat", Interval: 60, Timeout: 10, FailureThreshold: 1, SuccessThreshold: 1}},
		Groups:             []*storage.MonitorGroup{{ID: group, Name: "production", SortOrder: 2}},
		Tags:               []*storage.Tag{{ID: 9, Name: "owner", Color: "#808080"}},
		Proxies:            []*storage.Proxy{{ID: proxy, Name: "egress", Protocol: "http", Host: "proxy.test", Port: 8080, AuthUser: "user", AuthPass: "proxy-secret", Enabled: true}},
		Channels:           []*storage.NotificationChannel{{ID: 5, Name: "ops", Type: "webhook", Enabled: true, Settings: json.RawMessage(`{"url":"https://notify.test/secret"}`), Events: []string{"incident.created"}}},
		EscalationPolicies: []*storage.EscalationPolicy{{ID: policy, Name: "urgent", Enabled: true, Repeat: true, Steps: []*storage.EscalationPolicyStep{{StepOrder: 0, DelayMinutes: 5, NotificationChannelIDs: []int64{5}}}}},
		Rotations:          []*storage.OnCallRotation{{ID: 7, Name: "duty", ChannelIDs: []int64{5}, Period: "daily", PeriodStartedAt: anchor}},
		Maintenance:        []*storage.MaintenanceWindow{{ID: 10, Name: "maintenance", MonitorIDs: []int64{1}, StartTime: anchor, EndTime: end, Recurring: "cron", CronExpr: "0 2 * * *", Active: true}},
		StatusPages:        []*storage.StatusPage{{ID: 11, Slug: "public", Title: "Health", LogoURL: "https://example.test/logo.svg", FaviconURL: "https://example.test/favicon.ico", CustomHeaderHTML: "<p>Header</p>", PasswordHash: strings.Repeat("a", 64), PasswordEnabled: true, AnalyticsScript: "<script src=\"https://analytics.test/script.js\"></script>\n"}},
		StatusPageMonitors: []storage.StatusPageMonitor{{PageID: 11, MonitorID: 1, SortOrder: 2, GroupName: "Web"}},
		Agents:             []storage.ConfigurationAgent{{ID: 8, Name: "probe"}},
		HeartbeatGrace:     map[int64]int{2: 30},
	}
	file := configurationFile(c, false)
	body, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"created_at"`, `"updated_at"`, `"last_check_at"`, `"password_enabled"`, `"token"`, `"consec_fails"`, `"status"`} {
		if strings.Contains(string(body), field) {
			t.Fatalf("runtime field %s entered portable export", field)
		}
	}
	decoded, err := DecodeConfiguration(strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, decoded) {
		t.Fatal("v2 DTO conversion lost a configuration field or reference")
	}
	if err := validateImportedConfiguration(decoded); err != nil {
		t.Fatalf("valid exported configuration rejected: %v", err)
	}
}

func TestConfigurationRedactionRemovesStructuredSecretsAndRejectsImport(t *testing.T) {
	c := &storage.ConfigurationSnapshot{Monitors: []*storage.Monitor{transferMonitor()}, Proxies: []*storage.Proxy{{ID: 1, AuthUser: "user-secret", AuthPass: "proxy-secret"}}, Channels: []*storage.NotificationChannel{{ID: 1, Settings: json.RawMessage(`{"url":"channel-secret"}`)}}, StatusPages: []*storage.StatusPage{{ID: 1, PasswordHash: "hash-secret"}}}
	file := configurationFile(c, true)
	body, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if !file.Redacted || strings.Contains(string(body), "secret") || len(file.Monitors) != 1 || file.Monitors[0].Type != "http" {
		t.Fatal("structured redaction leaked a target, payload, assertion or credential")
	}
	if _, err := DecodeConfiguration(strings.NewReader(string(body))); err == nil || !strings.Contains(err.Error(), "sharing only") {
		t.Fatalf("redacted configuration imported: %v", err)
	}
	if string(c.Monitors[0].Settings) == "{}" || c.Proxies[0].AuthPass == "" || c.StatusPages[0].PasswordHash == "" {
		t.Fatal("redaction mutated source snapshot")
	}
}

func TestConfigurationDecoderRejectsAmbiguityAndMalformedInputs(t *testing.T) {
	for _, body := range []string{
		``, `null`, `[]`, `{"version":99}`, `{"version":2} {"version":2}`,
		`{"version":2,"version":2}`, `{"version":2,"Version":1}`,
		`{"version":2,"unknown":"private-value-must-not-leak"}`,
		`{"version":2,"monitors":[{"id":"private-value-must-not-leak"}]}`,
		`{"version":2,"monitors":[{"settings":{"headers":{"Token":"first","token":"second"}}}]}`,
		strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66),
	} {
		t.Run(body[:min(len(body), 40)], func(t *testing.T) {
			if _, err := DecodeConfiguration(strings.NewReader(body)); err == nil || strings.Contains(err.Error(), "private-value-must-not-leak") {
				t.Fatalf("invalid input accepted or leaked parser value: %v", err)
			}
		})
	}
}

func TestConfigurationLegacyAdapterPreservesSupportedNameReferences(t *testing.T) {
	legacy := &ExportData{Version: 1,
		MonitorGroups:        []*storage.MonitorGroup{{Name: "production"}},
		Proxies:              []ExportProxy{{Name: "egress", Protocol: "http", Host: "proxy.test", Port: 8080, AuthPass: "secret"}},
		NotificationChannels: []*storage.NotificationChannel{{Name: "ops", Type: "webhook", Settings: json.RawMessage(`{"url":"https://notify.test"}`)}},
		Monitors:             []ExportMonitor{{Name: "web", Type: "http", Target: "https://example.test", Interval: 60, Timeout: 10, FailureThreshold: 1, SuccessThreshold: 1, GroupName: "production", ProxyName: "egress", NotificationChannelNames: []string{"ops"}, Tags: []ExportMonitorTag{{TagName: "owner", Color: "#808080", Value: "platform"}}}},
		StatusPages:          []ExportStatusPage{{Slug: "public", Title: "Health", Monitors: []ExportStatusPageMon{{MonitorName: "web", SortOrder: 2}}}},
	}
	body, _ := json.Marshal(legacy)
	c, err := DecodeConfiguration(strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	if *c.Monitors[0].GroupID != c.Groups[0].ID || *c.Monitors[0].ProxyID != c.Proxies[0].ID || c.Monitors[0].NotificationChannelIDs[0] != c.Channels[0].ID || c.Monitors[0].MonitorTags[0].TagID != c.Tags[0].ID || c.StatusPageMonitors[0].MonitorID != c.Monitors[0].ID {
		t.Fatal("v1 references were not remapped")
	}
	if err := validateImportedConfiguration(c); err != nil {
		t.Fatal(err)
	}
	legacy.MaintenanceWindows = []*storage.MaintenanceWindow{{Name: "old", MonitorIDs: []int64{1}}}
	if _, err := legacyConfigurationSnapshot(legacy); err == nil || !strings.Contains(err.Error(), "v1 omitted monitor identities") {
		t.Fatalf("unmappable maintenance was silently applied: %v", err)
	}
	legacy.MaintenanceWindows = nil
	legacy.Monitors = append(legacy.Monitors, legacy.Monitors[0])
	if _, err := legacyConfigurationSnapshot(legacy); err == nil {
		t.Fatal("ambiguous v1 monitor names accepted")
	}
}

func TestConfigurationTypedValidationRejectsInvalidFields(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*storage.ConfigurationSnapshot)
	}{
		{"manual negative interval", func(c *storage.ConfigurationSnapshot) { c.Monitors[0].Type = "manual"; c.Monitors[0].Interval = -1 }},
		{"SLA out of range", func(c *storage.ConfigurationSnapshot) { c.Monitors[0].SLATarget = 101 }},
		{"unknown setting", func(c *storage.ConfigurationSnapshot) {
			c.Monitors[0].Settings = json.RawMessage(`{"mystery":"value"}`)
		}},
		{"setting wrong type", func(c *storage.ConfigurationSnapshot) { c.Monitors[0].Settings = json.RawMessage(`{"headers":[]}`) }},
		{"setting null", func(c *storage.ConfigurationSnapshot) { c.Monitors[0].Settings = json.RawMessage(`null`) }},
		{"unknown assertion", func(c *storage.ConfigurationSnapshot) {
			c.Monitors[0].Assertions = json.RawMessage(`{"groups":[{"conditions":[{"type":"unknown"}]}]}`)
		}},
		{"assertion invalid regex", func(c *storage.ConfigurationSnapshot) {
			c.Monitors[0].Assertions = json.RawMessage(`{"groups":[{"conditions":[{"type":"body_regex","value":"["}]}]}`)
		}},
		{"numeric assertion wrong type", func(c *storage.ConfigurationSnapshot) {
			c.Monitors[0].Assertions = json.RawMessage(`{"groups":[{"conditions":[{"type":"status_code","value":"abc"}]}]}`)
		}},
		{"incomplete webhook", func(c *storage.ConfigurationSnapshot) {
			c.Channels = []*storage.NotificationChannel{{ID: 2, Name: "ops", Type: "webhook", Settings: json.RawMessage(`{}`)}}
		}},
		{"invalid hash", func(c *storage.ConfigurationSnapshot) {
			c.StatusPages = []*storage.StatusPage{{ID: 3, Slug: "public", Title: "Health", PasswordHash: "invalid"}}
		}},
		{"unsafe markup", func(c *storage.ConfigurationSnapshot) {
			c.StatusPages = []*storage.StatusPage{{ID: 3, Slug: "public", Title: "Health", CustomHeaderHTML: "<script>alert(1)</script>"}}
		}},
		{"encrypted proxy secret", func(c *storage.ConfigurationSnapshot) {
			c.Proxies = []*storage.Proxy{{ID: 4, Name: "proxy", Protocol: "http", Host: "proxy.test", Port: 8080, AuthPass: "enc:unavailable"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &storage.ConfigurationSnapshot{Monitors: []*storage.Monitor{transferMonitor()}}
			tc.edit(c)
			if err := validateImportedConfiguration(c); err == nil {
				t.Fatal("invalid imported configuration accepted")
			}
		})
	}
}

func TestConfigurationMultiStepVariableURLsRemainPortable(t *testing.T) {
	m := transferMonitor()
	m.Type = "http_multi"
	m.Settings = json.RawMessage(`{"steps":[{"url":"https://example.test","extract_var":"next","extract_json":"next"},{"url":"{{next}}/result"}]}`)
	if err := validateImportedMonitor(m); err != nil {
		t.Fatal(err)
	}
	m.Settings = json.RawMessage(`{"steps":[{"url":"{{unknown}}/result"}]}`)
	if err := validateImportedMonitor(m); err == nil {
		t.Fatal("undefined extracted variable accepted")
	}
}

func TestConfigurationAgentMappingRejectsAmbiguity(t *testing.T) {
	for _, raw := range []string{"1", "1:2:3", "0:1", "1:0", "1:2,1:3", "invalid:2"} {
		if _, err := ParseConfigurationAgentMapping(raw); err == nil {
			t.Fatalf("invalid mapping accepted: %q", raw)
		}
	}
	got, err := ParseConfigurationAgentMapping(" 91:2, 92:3 ")
	if err != nil || !reflect.DeepEqual(got, map[int64]int64{91: 2, 92: 3}) {
		t.Fatalf("mapping=%v error=%v", got, err)
	}
}

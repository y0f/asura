package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func configurationFixture() *ConfigurationSnapshot {
	group, proxy, policy, override := int64(11), int64(21), int64(41), int64(32)
	anchor := time.Now().UTC().Truncate(time.Second).Add(-3 * time.Hour)
	until := anchor.Add(12 * time.Hour)
	return &ConfigurationSnapshot{
		Groups:  []*MonitorGroup{{ID: group, Name: "production", SortOrder: 7}},
		Tags:    []*Tag{{ID: 12, Name: "owner", Color: "#808080"}, {ID: 13, Name: "unused", Color: "#0080ff"}},
		Proxies: []*Proxy{{ID: proxy, Name: "egress", Protocol: "http", Host: "proxy.test", Port: 8080, AuthUser: "service", AuthPass: "proxy-secret", Enabled: true}},
		Channels: []*NotificationChannel{
			{ID: 31, Name: "primary", Type: "webhook", Enabled: true, Settings: json.RawMessage(`{"url":"https://notify.test/secret"}`), Events: []string{"incident.created"}},
			{ID: 32, Name: "secondary", Type: "webhook", Settings: json.RawMessage(`{"url":"https://backup.test/secret"}`)},
		},
		EscalationPolicies: []*EscalationPolicy{{ID: policy, Name: "production", Description: "route", Enabled: true, Repeat: true, Steps: []*EscalationPolicyStep{{StepOrder: 0, DelayMinutes: 4, NotificationChannelIDs: []int64{32, 31}}}}},
		Monitors: []*Monitor{
			{ID: 51, Name: "same display name", Description: "first", Type: "http", Target: "https://example.test", Interval: 60, Timeout: 10, Enabled: true, Tags: []string{"legacy"}, Settings: json.RawMessage(`{"basic_auth_pass":"monitor-secret"}`), Assertions: json.RawMessage(`[]`), TrackChanges: true, FailureThreshold: 3, SuccessThreshold: 2, UpsideDown: true, ResendInterval: 10, SLATarget: 99.9, AnomalySensitivity: "high", GroupID: &group, ProxyID: &proxy, EscalationPolicyID: &policy, NotificationChannelIDs: []int64{32, 31}, MonitorTags: []MonitorTag{{TagID: 12, Value: "platform"}}},
			{ID: 52, Name: "same display name", Description: "heartbeat", Type: "heartbeat", Interval: 60, Timeout: 10, Enabled: true, GroupID: &group},
		},
		Rotations:          []*OnCallRotation{{ID: 61, Name: "duty", ChannelIDs: []int64{32, 31}, Period: "daily", CurrentIndex: 1, PeriodStartedAt: anchor, OverrideChannelID: &override, OverrideUntil: &until}},
		Maintenance:        []*MaintenanceWindow{{ID: 71, Name: "weekly", MonitorIDs: []int64{52, 51}, StartTime: anchor, EndTime: until, Recurring: "cron", CronExpr: "0 2 * * 0", Active: true}},
		StatusPages:        []*StatusPage{{ID: 81, Slug: "public", Title: "Service health", Description: "description", CustomCSS: ".brand{color:blue}", ShowIncidents: true, Enabled: true, APIEnabled: true, SortOrder: 3, LogoURL: "https://example.test/logo.svg", FaviconURL: "https://example.test/favicon.ico", CustomHeaderHTML: "<p>Service health</p>", PasswordHash: "$fixture-hash", PasswordEnabled: true, AnalyticsScript: "<script>void 0</script>"}},
		StatusPageMonitors: []StatusPageMonitor{{PageID: 81, MonitorID: 52, SortOrder: 1, GroupName: "Jobs"}, {PageID: 81, MonitorID: 51, SortOrder: 2, GroupName: "Web"}},
		HeartbeatGrace:     map[int64]int{52: 37},
	}
}

// Storage tests deliberately use a no-I/O validator: typed API validation is
// exercised separately. This keeps the transactional/reference tests focused.
func configurationOptions(mode string) ConfigurationImportOptions {
	return ConfigurationImportOptions{Mode: mode, Validate: func(*ConfigurationSnapshot) error { return nil }}
}

func TestConfigurationRoundTripRemapsEveryRelationship(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	data := configurationFixture()
	original := cloneConfiguration(data)
	result, err := s.ApplyConfiguration(ctx, data, configurationOptions("replace"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(data, original) {
		t.Fatal("import mutated caller-owned configuration")
	}
	if result.Monitors != 2 || result.Groups != 1 || result.Tags != 2 || result.Channels != 2 || result.Escalations != 1 || result.Rotations != 1 || result.Maintenance != 1 || result.StatusPages != 1 || result.Proxies != 1 || result.Skipped != 0 || result.Errors != 0 || len(result.Heartbeats) != 1 {
		t.Fatalf("incomplete result: %+v", result)
	}
	got, err := s.SnapshotConfiguration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Monitors) != 2 || got.Monitors[0].ID == data.Monitors[0].ID || got.Monitors[0].Name != got.Monitors[1].Name {
		t.Fatal("file-local IDs or duplicate display names not preserved correctly")
	}
	m := got.Monitors[0]
	if *m.GroupID != got.Groups[0].ID || *m.ProxyID != got.Proxies[0].ID || *m.EscalationPolicyID != got.EscalationPolicies[0].ID || m.MonitorTags[0].TagID != got.Tags[0].ID || m.MonitorTags[0].Value != "platform" || len(got.Tags) != 2 || !reflect.DeepEqual(m.NotificationChannelIDs, []int64{got.Channels[0].ID, got.Channels[1].ID}) {
		t.Fatal("monitor relations or unused tags lost")
	}
	if m.Settings == nil || string(m.Settings) != string(data.Monitors[0].Settings) || m.AnomalySensitivity != "high" || m.SLATarget != 99.9 || !m.UpsideDown || !m.TrackChanges || m.FailureThreshold != 3 || m.SuccessThreshold != 2 || m.ResendInterval != 10 || m.Tags[0] != "legacy" {
		t.Fatal("monitor configuration changed")
	}
	if got.Proxies[0].AuthPass != "proxy-secret" || string(got.Channels[0].Settings) != string(data.Channels[0].Settings) || got.StatusPages[0].PasswordHash != "$fixture-hash" || got.StatusPages[0].CustomHeaderHTML != data.StatusPages[0].CustomHeaderHTML || got.StatusPages[0].AnalyticsScript != data.StatusPages[0].AnalyticsScript || got.StatusPages[0].LogoURL != data.StatusPages[0].LogoURL || !got.StatusPages[0].PasswordEnabled {
		t.Fatal("credentials or page branding/protection lost")
	}
	channels := []int64{got.Channels[1].ID, got.Channels[0].ID}
	if !reflect.DeepEqual(got.EscalationPolicies[0].Steps[0].NotificationChannelIDs, channels) || !reflect.DeepEqual(got.Rotations[0].ChannelIDs, channels) || *got.Rotations[0].OverrideChannelID != got.Channels[1].ID || got.Rotations[0].CurrentIndex != 1 || !got.Rotations[0].PeriodStartedAt.Equal(data.Rotations[0].PeriodStartedAt) || !got.Rotations[0].OverrideUntil.Equal(*data.Rotations[0].OverrideUntil) {
		t.Fatal("escalation routing or rotation phase lost")
	}
	if !reflect.DeepEqual(got.Maintenance[0].MonitorIDs, []int64{got.Monitors[1].ID, got.Monitors[0].ID}) || got.Maintenance[0].CronExpr != data.Maintenance[0].CronExpr || !got.Maintenance[0].Active || got.StatusPageMonitors[0].MonitorID != got.Monitors[1].ID || got.StatusPageMonitors[0].PageID != got.StatusPages[0].ID || got.StatusPageMonitors[0].GroupName != "Jobs" {
		t.Fatal("maintenance/page references lost")
	}
	hb := result.Heartbeats[0]
	stored, err := s.GetHeartbeatByToken(ctx, hb.Token)
	if err != nil || stored.MonitorID != hb.MonitorID || stored.Grace != 37 || stored.Token == hb.Token {
		t.Fatalf("fresh credential not hashed and linked atomically: %v", err)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), hb.Token) || strings.Contains(string(encoded), stored.Token) {
		t.Fatal("snapshot leaked reusable or hashed heartbeat credentials")
	}
	// A second destination creates its own credential and retains the phase.
	destination := testStore(t)
	second, err := destination.ApplyConfiguration(ctx, got, configurationOptions("replace"))
	if err != nil || len(second.Heartbeats) != 1 || second.Heartbeats[0].Token == hb.Token {
		t.Fatalf("snapshot cannot be imported with fresh enrollment: %v", err)
	}
}

func TestConfigurationReplacementRollbackIncludesCascades(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"merge", "replace"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			old := configurationFixture()
			old.Monitors[0].Name, old.Monitors[1].Name = "old web", "old heartbeat"
			if _, err := s.ApplyConfiguration(ctx, old, configurationOptions("replace")); err != nil {
				t.Fatal(err)
			}
			before, err := s.SnapshotConfiguration(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.writeDB.ExecContext(ctx, "INSERT INTO check_results (monitor_id,status,created_at) VALUES (?, 'up', ?)", before.Monitors[0].ID, formatTime(time.Now())); err != nil {
				t.Fatal(err)
			}
			if _, err := s.writeDB.ExecContext(ctx, "CREATE TRIGGER reject_import_page BEFORE INSERT ON status_pages BEGIN SELECT RAISE(ABORT,'injected final write failure'); END"); err != nil {
				t.Fatal(err)
			}
			incoming := configurationFixture()
			incoming.StatusPages[0].Slug = "new-page"
			result, err := s.ApplyConfiguration(ctx, incoming, configurationOptions(mode))
			if err == nil || result != nil {
				t.Fatalf("failed import exposed partial success: result=%+v error=%v", result, err)
			}
			after, err := s.SnapshotConfiguration(ctx)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("failed import changed configuration: %v", err)
			}
			var count int
			if err := s.readDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM check_results").Scan(&count); err != nil || count != 1 {
				t.Fatalf("failed replacement lost cascaded history: %d %v", count, err)
			}
		})
	}
}

func TestConfigurationPreflightRejectsInvalidReferencesBeforeWrites(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*ConfigurationSnapshot)
	}{
		{"null entity", func(c *ConfigurationSnapshot) { c.Monitors[0] = nil }},
		{"duplicate ID", func(c *ConfigurationSnapshot) { c.Monitors[1].ID = c.Monitors[0].ID }},
		{"missing group", func(c *ConfigurationSnapshot) { *c.Monitors[0].GroupID = 999 }},
		{"missing proxy", func(c *ConfigurationSnapshot) { *c.Monitors[0].ProxyID = 999 }},
		{"missing policy", func(c *ConfigurationSnapshot) { *c.Monitors[0].EscalationPolicyID = 999 }},
		{"missing channel", func(c *ConfigurationSnapshot) { c.Monitors[0].NotificationChannelIDs = []int64{999} }},
		{"missing tag", func(c *ConfigurationSnapshot) { c.Monitors[0].MonitorTags[0].TagID = 999 }},
		{"missing agent", func(c *ConfigurationSnapshot) { c.Monitors[0].AgentIDs = []int64{999} }},
		{"heartbeat missing grace", func(c *ConfigurationSnapshot) { delete(c.HeartbeatGrace, 52) }},
		{"heartbeat invalid grace", func(c *ConfigurationSnapshot) { c.HeartbeatGrace[52] = -1 }},
		{"heartbeat wrong type", func(c *ConfigurationSnapshot) { c.HeartbeatGrace[51] = 1 }},
		{"step missing channel", func(c *ConfigurationSnapshot) { c.EscalationPolicies[0].Steps[0].NotificationChannelIDs = []int64{999} }},
		{"duplicate step order", func(c *ConfigurationSnapshot) {
			c.EscalationPolicies[0].Steps = append(c.EscalationPolicies[0].Steps, &EscalationPolicyStep{StepOrder: 0})
		}},
		{"rotation missing channel", func(c *ConfigurationSnapshot) { c.Rotations[0].ChannelIDs = []int64{999, 31} }},
		{"rotation missing anchor", func(c *ConfigurationSnapshot) { c.Rotations[0].PeriodStartedAt = time.Time{} }},
		{"maintenance missing monitor", func(c *ConfigurationSnapshot) { c.Maintenance[0].MonitorIDs = []int64{999} }},
		{"page missing monitor", func(c *ConfigurationSnapshot) { c.StatusPageMonitors[0].MonitorID = 999 }},
		{"page missing page", func(c *ConfigurationSnapshot) { c.StatusPageMonitors[0].PageID = 999 }},
		{"page duplicate membership", func(c *ConfigurationSnapshot) {
			c.StatusPageMonitors = append(c.StatusPageMonitors, c.StatusPageMonitors[0])
		}},
		{"invalid settings", func(c *ConfigurationSnapshot) { c.Monitors[0].Settings = json.RawMessage(`invalid`) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			if _, err := s.writeDB.Exec("CREATE TRIGGER no_writes BEFORE INSERT ON monitor_groups BEGIN SELECT RAISE(ABORT,'preflight wrote before validation'); END"); err != nil {
				t.Fatal(err)
			}
			c := configurationFixture()
			tc.edit(c)
			result, err := s.ApplyConfiguration(context.Background(), c, configurationOptions("replace"))
			if !errors.Is(err, ErrInvalidConfiguration) || result != nil {
				t.Fatalf("invalid reference reached writes or returned partial result: %+v %v", result, err)
			}
		})
	}
}

func TestConfigurationMergePreservesExistingSecretsAndRejectsAmbiguity(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	c := configurationFixture()
	c.Monitors[0].Name, c.Monitors[1].Name = "web", "heartbeat"
	first, err := s.ApplyConfiguration(ctx, c, configurationOptions("replace"))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.SnapshotConfiguration(ctx)
	c.Proxies[0].AuthPass = "changed"
	c.Channels[0].Settings = json.RawMessage(`{"url":"https://changed.test"}`)
	c.HeartbeatGrace[52] = 100
	result, err := s.ApplyConfiguration(ctx, c, configurationOptions("merge"))
	if err != nil || result.Skipped != 12 || result.Monitors != 0 || len(result.Heartbeats) != 0 {
		t.Fatalf("merge did not preserve all existing entities: %+v %v", result, err)
	}
	after, _ := s.SnapshotConfiguration(ctx)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("merge overwrote existing configuration")
	}
	if _, err := s.GetHeartbeatByToken(ctx, first.Heartbeats[0].Token); err != nil {
		t.Fatal("merge replaced an existing heartbeat credential")
	}
	duplicate := relationErrorMonitor()
	duplicate.Name = "web"
	if err := s.CreateMonitor(ctx, duplicate); err != nil {
		t.Fatal(err)
	}
	if result, err := s.ApplyConfiguration(ctx, c, configurationOptions("merge")); !errors.Is(err, ErrInvalidConfiguration) || result != nil {
		t.Fatalf("ambiguous destination name accepted: %v", err)
	}
}

func TestConfigurationReplaceRemovesChannelsButPreservesEnrollments(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if _, err := s.ApplyConfiguration(ctx, configurationFixture(), configurationOptions("replace")); err != nil {
		t.Fatal(err)
	}
	a := &Agent{Name: "destination", Token: "destination-secret", Enabled: true}
	if err := s.CreateAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyConfiguration(ctx, &ConfigurationSnapshot{}, configurationOptions("replace")); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"monitors", "monitor_groups", "tags", "proxies", "notification_channels", "escalation_policies", "escalation_policy_steps", "maintenance_windows", "status_pages", "status_page_monitors", "on_call_rotations", "heartbeats"} {
		var count int
		if err := s.readDB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("replace retained %s: %d %v", table, count, err)
		}
	}
	if got, err := s.GetAgentByToken(ctx, a.Token); err != nil || got.ID != a.ID {
		t.Fatalf("replace changed enrollment: %v", err)
	}
}

func TestConfigurationSnapshotHasNoMonitorPaginationCap(t *testing.T) {
	s := testStore(t)
	tx, err := s.writeDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("INSERT INTO monitors (name,type,target,interval_secs,timeout_secs,enabled,agent_enabled,tags,settings,assertions,created_at,updated_at) VALUES (?,'http','https://example.test',60,10,1,0,'[]','{}','[]',?,?)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	now := formatTime(time.Now())
	for i := range 10001 {
		if _, err := stmt.Exec(fmt.Sprintf("monitor %d", i), now, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.SnapshotConfiguration(context.Background())
	if err != nil || len(snapshot.Monitors) != 10001 {
		t.Fatalf("snapshot truncated: %v", err)
	}
}

func TestConfigurationSnapshotFailsOnUnreadableConfiguration(t *testing.T) {
	for _, update := range []string{
		"UPDATE monitors SET settings='not JSON'", "UPDATE monitors SET assertions='not JSON'", "UPDATE monitors SET tags='not JSON'", "UPDATE monitors SET group_id=999",
		"UPDATE notification_channels SET settings='not JSON'", "UPDATE notification_channels SET events='not JSON'", "UPDATE proxies SET auth_pass='enc:unavailable'",
		"UPDATE maintenance_windows SET monitor_ids='not JSON'", "UPDATE escalation_policy_steps SET notification_channel_ids='not JSON'", "DELETE FROM heartbeats",
	} {
		t.Run(update, func(t *testing.T) {
			s := testStore(t)
			if _, err := s.ApplyConfiguration(context.Background(), configurationFixture(), configurationOptions("replace")); err != nil {
				t.Fatal(err)
			}
			if _, err := s.writeDB.Exec(update); err != nil {
				t.Fatal(err)
			}
			if result, err := s.SnapshotConfiguration(context.Background()); err == nil || result != nil {
				t.Fatal("corrupt configuration returned a successful partial snapshot")
			}
		})
	}
}

func TestConfigurationValidationFailurePreservesInputAndDatabase(t *testing.T) {
	s := testStore(t)
	c := configurationFixture()
	options := configurationOptions("replace")
	options.Validate = func(private *ConfigurationSnapshot) error {
		private.Monitors[0].Name = "mutated"
		return errors.New("typed validation rejected")
	}
	if result, err := s.ApplyConfiguration(context.Background(), c, options); !errors.Is(err, ErrInvalidConfiguration) || result != nil || c.Monitors[0].Name != "same display name" {
		t.Fatalf("failed validation changed input or returned success: %v", err)
	}
	var count int
	if err := s.readDB.QueryRow("SELECT COUNT(*) FROM monitors").Scan(&count); err != nil || count != 0 {
		t.Fatalf("validation wrote rows: %d %v", count, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := s.ApplyConfiguration(ctx, c, configurationOptions("replace")); !errors.Is(err, context.Canceled) || result != nil {
		t.Fatalf("canceled import succeeded: %v", err)
	}
	if _, err := s.GetMonitor(context.Background(), 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("canceled import wrote monitor: %v", err)
	}
}

func TestConfigurationAgentMappingsNeverCreateOrWidenEnrollments(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"merge", "replace"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			first, second := &Agent{Name: "probe", Enabled: true}, &Agent{Name: "probe", Enabled: false}
			for _, a := range []*Agent{first, second} {
				if err := s.CreateAgent(ctx, a); err != nil {
					t.Fatal(err)
				}
			}
			c := configurationFixture()
			c.Agents = []ConfigurationAgent{{ID: 91, Name: "probe"}, {ID: 92, Name: "unused source enrollment"}}
			c.Monitors[0].AgentEnabled, c.Monitors[0].AgentIDs = true, []int64{91}
			c.Monitors[0].Settings = json.RawMessage(`{"headers":{"X-API-Key":"explicit request data"}}`)
			options := configurationOptions(mode)
			if result, err := s.ApplyConfiguration(ctx, c, options); !errors.Is(err, ErrInvalidConfiguration) || result != nil {
				t.Fatalf("ambiguous name widened assignments: %v", err)
			}
			options.AgentIDs = map[int64]int64{91: 99999}
			if result, err := s.ApplyConfiguration(ctx, c, options); !errors.Is(err, ErrInvalidConfiguration) || result != nil {
				t.Fatalf("missing enrollment silently accepted: %v", err)
			}
			options.AgentIDs[91] = second.ID
			if _, err := s.ApplyConfiguration(ctx, c, options); err != nil {
				t.Fatal(err)
			}
			got, err := s.SnapshotConfiguration(ctx)
			if err != nil || !reflect.DeepEqual(got.Monitors[0].AgentIDs, []int64{second.ID}) {
				t.Fatalf("explicit destination ignored: %v", err)
			}
			if agents, err := s.ListAgents(ctx); err != nil || len(agents) != 2 || agents[1].Enabled {
				t.Fatalf("import created or enabled an enrollment: %v", err)
			}
			if _, err := s.GetAgentByToken(ctx, second.Token); err != nil {
				t.Fatal("destination token changed")
			}
		})
	}
}

func TestConfigurationEncryptsDestinationSecretsAndFailsWrongKeyExport(t *testing.T) {
	s := testStore(t)
	gcm, err := newGCM([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	s.enc = &Encryptor{gcm: gcm, legacyGCM: gcm}
	if _, err := s.ApplyConfiguration(context.Background(), configurationFixture(), configurationOptions("replace")); err != nil {
		t.Fatal(err)
	}
	for _, field := range []struct{ table, column string }{{"monitors", "settings"}, {"notification_channels", "settings"}, {"proxies", "auth_pass"}} {
		var value string
		if err := s.readDB.QueryRow("SELECT " + field.column + " FROM " + field.table + " ORDER BY id LIMIT 1").Scan(&value); err != nil || !strings.HasPrefix(value, encPrefix) || strings.Contains(value, "secret") {
			t.Fatalf("%s not encrypted at destination: %v", field.table, err)
		}
	}
	if got, err := s.SnapshotConfiguration(context.Background()); err != nil || got.Proxies[0].AuthPass != "proxy-secret" {
		t.Fatalf("encrypted round trip failed: %v", err)
	}
	wrong, err := newGCM([]byte("abcdef0123456789abcdef0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	s.enc = &Encryptor{gcm: wrong, legacyGCM: wrong}
	if got, err := s.SnapshotConfiguration(context.Background()); err == nil || got != nil {
		t.Fatal("wrong-key export returned partial configuration")
	}
}

func TestConfigurationProxyPasswordPrefixUsesDestinationEncryption(t *testing.T) {
	ctx := context.Background()
	const password = "enc:literal-password"
	input := configurationFixture()
	input.Proxies[0].AuthPass = password
	for _, encrypted := range []bool{false, true} {
		t.Run(fmt.Sprint(encrypted), func(t *testing.T) {
			s := testStore(t)
			if _, err := s.ApplyConfiguration(ctx, configurationFixture(), configurationOptions("replace")); err != nil {
				t.Fatal(err)
			}
			if encrypted {
				gcm, err := newGCM([]byte("0123456789abcdef0123456789abcdef"))
				if err != nil {
					t.Fatal(err)
				}
				s.enc = &Encryptor{gcm: gcm, legacyGCM: gcm}
			}
			result, err := s.ApplyConfiguration(ctx, input, configurationOptions("replace"))
			if !encrypted {
				if !errors.Is(err, ErrInvalidConfiguration) || result != nil || strings.Contains(err.Error(), password) {
					t.Fatalf("ambiguous stored credential accepted or exposed: %v", err)
				}
				got, err := s.SnapshotConfiguration(ctx)
				if err != nil || got.Proxies[0].AuthPass != "proxy-secret" {
					t.Fatalf("rejected replacement changed configuration: %v", err)
				}
				return
			}
			if err != nil || result.Proxies != 1 {
				t.Fatalf("literal password rejected despite encryption: %v", err)
			}
			got, err := s.SnapshotConfiguration(ctx)
			if err != nil || got.Proxies[0].AuthPass != password {
				t.Fatalf("literal password did not round-trip: %v", err)
			}
			proxy, err := s.GetProxy(ctx, got.Proxies[0].ID)
			if err != nil || proxy.AuthPass != password {
				t.Fatalf("imported literal credential is not usable: %v", err)
			}
		})
	}
}

func TestConfigurationSnapshotPreservesInertLegacyDelegationFlags(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.ApplyConfiguration(ctx, configurationFixture(), configurationOptions("replace")); err != nil {
		t.Fatal(err)
	}
	// Pre-v39 data can have agent_enabled=true with no assignment, typed auth,
	// or a server-only heartbeat type. Runtime job selection keeps these inert.
	if _, err := s.writeDB.Exec("UPDATE monitors SET agent_enabled=1"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.SnapshotConfiguration(ctx)
	if err != nil || len(snapshot.Monitors) != 2 || !snapshot.Monitors[0].AgentEnabled || !snapshot.Monitors[1].AgentEnabled {
		t.Fatalf("inert legacy flags prevented or changed export: %v", err)
	}
	if _, err := s.ApplyConfiguration(ctx, snapshot, configurationOptions("replace")); err != nil {
		t.Fatalf("inert configuration must remain portable: %v", err)
	}
}

func TestConfigurationAgentJobEligibilitySurvivesFullRoundTrip(t *testing.T) {
	ctx := context.Background()
	source, destination := testStore(t), testStore(t)
	sourceAgent, destinationAgent := &Agent{Name: "probe", Enabled: true}, &Agent{Name: "probe", Enabled: true}
	if err := source.CreateAgent(ctx, sourceAgent); err != nil {
		t.Fatal(err)
	}
	// A different ID at the destination proves assignments are actually remapped.
	if err := destination.CreateAgent(ctx, &Agent{Name: "unrelated", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := destination.CreateAgent(ctx, destinationAgent); err != nil {
		t.Fatal(err)
	}
	c := configurationFixture()
	c.Monitors[0].Name, c.Monitors[1].Name = "typed auth", "heartbeat"
	c.Agents = []ConfigurationAgent{{ID: 91, Name: "probe"}}
	for _, m := range c.Monitors {
		m.AgentEnabled, m.AgentIDs = true, []int64{91}
	}
	for i, name := range []string{"eligible request data", "unassigned", "delegation disabled", "server proxy"} {
		m := copyMonitorSnapshot(c.Monitors[0])
		m.ID, m.Name = int64(53+i), name
		m.Settings = json.RawMessage(`{"headers":{"X-Token":"explicit request data"}}`)
		if name != "server proxy" {
			m.ProxyID = nil
		}
		if name == "unassigned" {
			m.AgentIDs = nil
		}
		if name == "delegation disabled" {
			m.AgentEnabled = false
		}
		c.Monitors = append(c.Monitors, m)
	}
	if _, err := source.ApplyConfiguration(ctx, c, configurationOptions("replace")); err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.SnapshotConfiguration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := destination.ApplyConfiguration(ctx, snapshot, configurationOptions("replace")); err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct {
		store *SQLiteStore
		agent *Agent
	}{{source, sourceAgent}, {destination, destinationAgent}} {
		jobs, err := pair.store.ListAgentJobs(ctx, pair.agent.ID)
		if err != nil || len(jobs) != 1 || jobs[0].Name != "eligible request data" {
			t.Fatalf("round trip changed effective delegation: jobs=%+v err=%v", jobs, err)
		}
		state, err := pair.store.SnapshotConfiguration(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range state.Monitors {
			eligible, err := pair.store.AgentMayCheckMonitor(ctx, pair.agent.ID, m.ID)
			if err != nil || eligible != (m.Name == "eligible request data") {
				t.Fatalf("agent result admission changed for %s: %v %v", m.Name, eligible, err)
			}
			if m.Name == "unassigned" {
				if !m.AgentEnabled || len(m.AgentIDs) != 0 {
					t.Fatal("inert flag or empty assignments changed")
				}
			} else if len(m.AgentIDs) != 1 || m.AgentIDs[0] != pair.agent.ID {
				t.Fatal("explicit assignment was lost or widened")
			}
		}
	}
}

func TestConfigurationCatalogsFollowValidatedIdentityNormalization(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	c := configurationFixture()
	options := configurationOptions("replace")
	options.Validate = func(private *ConfigurationSnapshot) error {
		private.Groups[0].ID = 101
		for _, m := range private.Monitors {
			*m.GroupID = 101
		}
		return nil
	}
	if _, err := s.ApplyConfiguration(ctx, c, options); err != nil {
		t.Fatalf("catalog used IDs from before validation: %v", err)
	}
	got, err := s.SnapshotConfiguration(ctx)
	if err != nil || len(got.Groups) != 1 || *got.Monitors[0].GroupID != got.Groups[0].ID || *got.Monitors[1].GroupID != got.Groups[0].ID {
		t.Fatalf("normalized identity mapping lost references: %v", err)
	}
	if c.Groups[0].ID != 11 || *c.Monitors[0].GroupID != 11 {
		t.Fatal("normalization changed caller-owned identities")
	}
}

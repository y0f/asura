package httputil

import (
	"errors"
	"testing"

	"github.com/y0f/asura/v2/internal/storage"
)

func TestMonitorCreateOptionsUseFreshHeartbeatCredentials(t *testing.T) {
	monitor := &storage.Monitor{Type: "heartbeat", Settings: []byte(`{"grace":15}`)}
	first, err := MonitorCreateOptions(monitor)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MonitorCreateOptions(monitor)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Heartbeat.Token) != 32 || first.Heartbeat.Token == second.Heartbeat.Token || first.Heartbeat.Grace != 15 || first.Heartbeat.ID != 0 || first.Heartbeat.MonitorID != 0 {
		t.Fatal("new heartbeat options lost freshness, grace, or uncommitted identity")
	}
	for _, raw := range []string{`{"grace":-1}`, `{"grace":1.5}`, `{"grace":"15"}`, `[]`, `{`} {
		if _, err := MonitorCreateOptions(&storage.Monitor{Type: "heartbeat", Settings: []byte(raw)}); err == nil {
			t.Errorf("invalid heartbeat settings accepted: %s", raw)
		}
	}
	if options, err := MonitorCreateOptions(&storage.Monitor{Type: "http"}); err != nil || options.Heartbeat != nil {
		t.Fatalf("ordinary monitor received heartbeat credential: %+v, %v", options, err)
	}
}

func TestPrepareMonitorCloneRequiresAuthorityForCopiedRelations(t *testing.T) {
	proxy := int64(3)
	source := &storage.Monitor{ID: 1, Name: "source", Type: "http", Target: "https://example.test", Enabled: true, ProxyID: &proxy}
	key := relationKey(t, []string{"monitors.clone"})
	if _, _, err := PrepareMonitorClone(key, source, "fresh-token"); !errors.Is(err, ErrMonitorRelationForbidden) {
		t.Fatalf("clone delegated unreadable proxy: %v", err)
	}
	key = relationKey(t, []string{"monitors.clone", "monitors.assign", "proxies.read"})
	clone, options, err := PrepareMonitorClone(key, source, "fresh-token")
	if err != nil || options.Heartbeat != nil || clone.Enabled || clone.ID != 0 || clone.Name != "source (copy)" || clone.ProxyID == nil || *clone.ProxyID != proxy {
		t.Fatalf("authorized clone lost configuration: %+v, %+v, %v", clone, options, err)
	}
	if source.ID != 1 || source.Name != "source" || !source.Enabled {
		t.Fatal("clone preparation modified original source")
	}
	source.Type, source.ProxyID, source.Settings = "heartbeat", nil, []byte(`{"grace":9}`)
	_, options, err = PrepareMonitorClone(key, source, "fresh-token")
	if err != nil || options.Heartbeat.Token != "fresh-token" || options.Heartbeat.Grace != 9 || options.Heartbeat.MonitorID != 0 {
		t.Fatalf("heartbeat clone did not use the fresh unbound credential: %+v, %v", options, err)
	}
}

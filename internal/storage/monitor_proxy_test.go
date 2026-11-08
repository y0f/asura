package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestProxyRoutingMutationsAreAtomic(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := &Proxy{Name: "egress", Protocol: "socks5", Host: "proxy.test", Port: 1080, Enabled: true}
	if err := s.CreateProxy(ctx, p); err != nil {
		t.Fatal(err)
	}
	m := newTestMonitor(t, s, "tcp route")
	m.Type, m.ProxyID = "tcp", &p.ID
	if err := s.UpdateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	revision := m.ConfigRevision
	if err := s.DeleteProxy(ctx, p.ID); !errors.Is(err, ErrProxyInUse) {
		t.Fatalf("implicit direct fallback: %v", err)
	}
	p.Protocol = "http"
	if err := s.UpdateProxy(ctx, p); !errors.Is(err, ErrInvalidMonitorProxy) {
		t.Fatalf("unsupported protocol change: %v", err)
	}
	savedProxy, err := s.GetProxy(ctx, p.ID)
	if err != nil || savedProxy.Protocol != "socks5" {
		t.Fatalf("rejected mutation changed proxy: %v", err)
	}
	saved, err := s.GetMonitor(ctx, m.ID)
	if err != nil || saved.ProxyID == nil || saved.ConfigRevision != revision {
		t.Fatalf("rejected mutation changed monitor: %v", err)
	}
	bad := *m
	bad.Type = "smtp"
	if err := s.UpdateMonitor(ctx, &bad); !errors.Is(err, ErrInvalidMonitorProxy) {
		t.Fatalf("unsupported type change: %v", err)
	}
	if err := s.UpdateMonitor(ctx, &bad, MonitorAccess{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("scope leaked proxy validation: %v", err)
	}
	bad.ID, bad.Name = 0, "invalid new route"
	if err := s.CreateMonitor(ctx, &bad); !errors.Is(err, ErrInvalidMonitorProxy) || bad.ID != 0 {
		t.Fatalf("unsupported create committed: %v", err)
	}
	bad.Type, bad.Settings = "dns", []byte(`{}`)
	if err := s.CreateMonitor(ctx, &bad); !errors.Is(err, ErrInvalidMonitorProxy) {
		t.Fatalf("DNS default resolver bypassed proxy: %v", err)
	}
	m.ProxyID = nil
	if err := s.UpdateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProxy(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
}

func TestProxyImportRejectsIncompatibleDestinationAndRollsBack(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	existing := newTestMonitor(t, s, "keep existing")
	data := configurationFixture()
	data.Monitors[0].Type = "tcp" // Imported proxy is HTTP.
	if result, err := s.ApplyConfiguration(ctx, data, configurationOptions("replace")); !errors.Is(err, ErrInvalidConfiguration) || result != nil {
		t.Fatalf("invalid routing import: %v", err)
	}
	if got, err := s.GetMonitor(ctx, existing.ID); err != nil || got.Name != existing.Name {
		t.Fatalf("rejected replacement erased configuration: %v", err)
	}
}

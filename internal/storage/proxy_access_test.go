package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProxyRoutingRequiresEveryCurrentMonitorScope(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := &Proxy{Name: "egress", Protocol: "http", Host: "proxy.test", Port: 8080, Enabled: true}
	if err := s.CreateProxy(ctx, p); err != nil {
		t.Fatal(err)
	}
	g := &MonitorGroup{Name: "allowed"}
	if err := s.CreateMonitorGroup(ctx, g); err != nil {
		t.Fatal(err)
	}
	m, other := newTestMonitor(t, s, "scoped"), newTestMonitor(t, s, "ungrouped")
	m.ProxyID, m.GroupID, other.ProxyID = &p.ID, &g.ID, &p.ID
	for _, monitor := range []*Monitor{m, other} {
		if err := s.UpdateMonitor(ctx, monitor); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range [][]MonitorAccess{
		{{}},
		{{GroupIDs: []int64{g.ID}}}, // NULL group membership must not pass NOT.
		{{All: true}, {MonitorIDs: []int64{m.ID}}},
		{{MonitorIDs: []int64{m.ID}}, {All: true}},
	} {
		for _, mutate := range []func(*Proxy){
			func(p *Proxy) { p.Host = "new.test" }, func(p *Proxy) { p.Port++ },
			func(p *Proxy) { p.Protocol = "socks5" }, func(p *Proxy) { p.AuthUser = "new-account" },
			func(p *Proxy) { p.AuthPass = "new-credential" },
		} {
			_, err := s.UpdateProxyWith(ctx, p.ID, func(current *Proxy) (*Proxy, error) { mutate(current); return current, nil }, scope...)
			if !errors.Is(err, ErrProxyRoutingForbidden) {
				t.Fatalf("unscoped route edit: %v", err)
			}
		}
	}
	got, err := s.GetProxy(ctx, p.ID)
	if err != nil || got.Host != p.Host || got.Protocol != p.Protocol || got.Port != p.Port || got.AuthUser != "" || got.AuthPass != "" {
		t.Fatalf("rejected routes changed state: %v", err)
	}
	// Resource administrators may manage display and availability without
	// gaining control of the destination receiving monitor credentials.
	if _, err := s.UpdateProxyWith(ctx, p.ID, func(current *Proxy) (*Proxy, error) {
		current.Name = "renamed"
		current.Enabled = false
		return current, nil
	}, MonitorAccess{}); err != nil {
		t.Fatal(err)
	}
	allowed := MonitorAccess{MonitorIDs: []int64{m.ID, other.ID}}
	if _, err := s.UpdateProxyWith(ctx, p.ID, func(current *Proxy) (*Proxy, error) { current.Host = "new.test"; return current, nil }, allowed, allowed); err != nil {
		t.Fatalf("authorized route blocked: %v", err)
	}
}

func TestProxyRoutingChecksAssignmentsAfterWaitingForWriter(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := &Proxy{Name: "egress", Protocol: "http", Host: "proxy.test", Port: 8080}
	if err := s.CreateProxy(ctx, p); err != nil {
		t.Fatal(err)
	}
	m := newTestMonitor(t, s, "not allowed")
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE monitors SET proxy_id=? WHERE id=?", p.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	waits := s.writeDB.Stats().WaitCount
	go func() {
		_, err := s.UpdateProxyWith(ctx, p.ID, func(current *Proxy) (*Proxy, error) { current.Host = "new.test"; return current, nil }, MonitorAccess{})
		done <- err
	}()
	for s.writeDB.Stats().WaitCount == waits {
		select {
		case err := <-done:
			t.Fatalf("route edit did not wait for held writer: %v", err)
		case <-ctx.Done():
			t.Fatal("route edit never entered writer wait")
		case <-time.After(time.Millisecond):
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrProxyRoutingForbidden) {
		t.Fatalf("newly assigned monitor bypassed route scope: %v", err)
	}
}

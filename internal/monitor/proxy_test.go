package monitor

import (
	"context"
	"net/url"
	"testing"

	"github.com/y0f/asura/v3/internal/checker"
	"github.com/y0f/asura/v3/internal/storage"
)

func TestSchedulerProxyIPv6AndUnavailableFailClosed(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := &storage.Proxy{Name: "IPv6 egress", Protocol: "http", Host: "2001:db8::1", Port: 8080, AuthUser: "user", AuthPass: "secret@password", Enabled: true}
	if err := s.CreateProxy(ctx, p); err != nil {
		t.Fatal(err)
	}
	m := &storage.Monitor{Type: "http", ProxyID: &p.ID}
	scheduler := NewScheduler(s, make(chan Job), discardLogger())
	scheduler.resolveProxyURLs(ctx, []*storage.Monitor{m})
	u, err := url.Parse(m.ProxyURL)
	if err != nil || u.Host != "[2001:db8::1]:8080" {
		t.Fatalf("invalid IPv6 proxy: %v", err)
	}
	password, _ := u.User.Password()
	if password != p.AuthPass {
		t.Fatal("proxy URL corrupted credentials")
	}
	p.Enabled = false
	if err := s.UpdateProxy(ctx, p); err != nil {
		t.Fatal(err)
	}
	scheduler.resolveProxyURLs(ctx, []*storage.Monitor{m})
	if m.ProxyURL != "" {
		t.Fatal("disabled proxy retained resolved route")
	}
	c, err := checker.DefaultRegistry(nil, true).Get(m.Type)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Check(ctx, m); err == nil {
		t.Fatal("disabled proxy fell back to direct checker")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	scheduler.resolveProxyURLs(ctx, []*storage.Monitor{m})
	if _, err := c.Check(ctx, m); err == nil {
		t.Fatal("unreadable proxy fell back to direct checker")
	}
}

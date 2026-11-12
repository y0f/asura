package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/storage"
)

func TestProxyRoutingAPIRejectsUnsupportedAndReferencedMutations(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	ctx := context.Background()
	p := &storage.Proxy{Name: "egress", Protocol: "socks5", Host: "proxy.test", Port: 1080, Enabled: true}
	if err := env.store.CreateProxy(ctx, p); err != nil {
		t.Fatal(err)
	}
	m := &storage.Monitor{Name: "tcp", Type: "tcp", Target: "example.test:443", Interval: 60, Timeout: 10, Enabled: true, ProxyID: &p.ID}
	if err := env.store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/proxies/%d", p.ID)
	if w := env.do("DELETE", path, "", admin); w.Code != http.StatusConflict {
		t.Fatalf("assigned delete: %d %s", w.Code, w.Body.String())
	}
	if w := env.do("PUT", path, `{"protocol":"http"}`, admin); w.Code != http.StatusBadRequest {
		t.Fatalf("incompatible protocol: %d %s", w.Code, w.Body.String())
	}
	body := fmt.Sprintf(`{"name":"unsupported","type":"smtp","target":"mail.test","interval":60,"timeout":10,"proxy_id":%d}`, p.ID)
	if w := env.do("POST", "/api/v1/monitors", body, admin); w.Code != http.StatusBadRequest {
		t.Fatalf("incompatible monitor: %d %s", w.Code, w.Body.String())
	}
	got, err := env.store.GetMonitor(ctx, m.ID)
	if err != nil || got.ProxyID == nil || *got.ProxyID != p.ID {
		t.Fatalf("rejected HTTP mutation changed routing: %v", err)
	}
}

func TestProxyRoutingAPIRequiresMonitorAssignmentAuthority(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) {
		c.Auth.APIKeys = append(c.Auth.APIKeys,
			config.APIKeyConfig{Name: "proxy-only", Hash: config.HashAPIKey("proxy-only"), Permissions: []string{"proxies.update"}},
			config.APIKeyConfig{Name: "router", Hash: config.HashAPIKey("router"), Permissions: []string{"proxies.update", "monitors.update", "monitors.assign"}})
	}, false, nil)
	ctx := context.Background()
	p := &storage.Proxy{Name: "egress", Protocol: "http", Host: "proxy.test", Port: 8080}
	if err := env.store.CreateProxy(ctx, p); err != nil {
		t.Fatal(err)
	}
	m := &storage.Monitor{Name: "private-monitor", Type: "http", Target: "https://private.test", Interval: 60, Timeout: 10, ProxyID: &p.ID}
	if err := env.store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/proxies/%d", p.ID)
	w := env.do("PUT", path, `{"host":"receiver.test"}`, map[string]string{"X-API-Key": "proxy-only"})
	if w.Code != 403 || strings.Contains(w.Body.String(), m.Name) || strings.Contains(w.Body.String(), m.Target) {
		t.Fatalf("proxy route scope: %d %s", w.Code, w.Body.String())
	}
	w = env.do("PUT", path, `{"host":"receiver.test"}`, map[string]string{"X-API-Key": "router"})
	if w.Code != 200 || w.Body.String() != fmt.Sprintf("{\"id\":%d}\n", p.ID) {
		t.Fatalf("authorized write-only route: %d %s", w.Code, w.Body.String())
	}
}

func TestProxyPasswordsRoundTripWithIndependentReadAuthority(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) {
		c.Auth.APIKeys = append(c.Auth.APIKeys,
			config.APIKeyConfig{Name: "writer", Hash: config.HashAPIKey("writer"), Permissions: []string{"proxies.create", "proxies.update", "proxies.delete"}},
			config.APIKeyConfig{Name: "reader", Hash: config.HashAPIKey("reader"), Permissions: []string{"proxies.read"}})
	}, false, nil)
	writer := map[string]string{"X-API-Key": "writer"}
	w := env.do("POST", "/api/v1/proxies", `{"name":"private-proxy","protocol":"http","host":"proxy.test","port":8080,"auth_user":"user","auth_pass":"saved-secret","enabled":true}`, writer)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response) != 1 || response["id"] == nil {
		t.Fatalf("write-only create response exposed data: %s", w.Body.String())
	}
	var id int64
	if err := json.Unmarshal(response["id"], &id); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/proxies/%d", id)
	assertStored := func(host, password string) {
		t.Helper()
		got, err := env.store.GetProxy(context.Background(), id)
		if err != nil || got.Host != host || got.AuthPass != password || !got.Enabled {
			t.Fatalf("stored proxy fields or password changed unexpectedly: %v", err)
		}
	}
	assertStored("proxy.test", "saved-secret")
	w = env.do("GET", path, "", map[string]string{"X-API-Key": "reader"})
	if w.Code != 200 || strings.Contains(w.Body.String(), "saved-secret") || strings.Contains(w.Body.String(), "auth_pass") {
		t.Fatalf("read response contains credential: %d %s", w.Code, w.Body.String())
	}
	var editable storage.Proxy
	if err := json.Unmarshal(w.Body.Bytes(), &editable); err != nil {
		t.Fatal(err)
	}
	editable.Name = "renamed"
	body, _ := json.Marshal(editable)
	w = env.do("PUT", path, string(body), writer)
	if w.Code != 200 || w.Body.String() != fmt.Sprintf("{\"id\":%d}\n", id) {
		t.Fatalf("write-only redacted round trip: %d %s", w.Code, w.Body.String())
	}
	assertStored("proxy.test", "saved-secret")
	for _, body := range []string{`{"host":"other.test"}`, `{"port":8081}`, `{"protocol":"socks5"}`, `{"auth_user":"another-user"}`, `{"host":"other.test","auth_pass":""}`} {
		w = env.do("PUT", path, body, writer)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "credential destination") || strings.Contains(w.Body.String(), "saved-secret") {
			t.Fatalf("retarget retained hidden password: %d %s", w.Code, w.Body.String())
		}
		assertStored("proxy.test", "saved-secret")
	}
	w = env.do("PUT", path, `{"host":"other.test","auth_pass":"replacement-secret"}`, writer)
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("replacement: %d %s", w.Code, w.Body.String())
	}
	assertStored("other.test", "replacement-secret")
	w = env.do("PUT", path, `{"host":"third.test","clear_auth_pass":true}`, writer)
	if w.Code != 200 {
		t.Fatalf("clear: %d %s", w.Code, w.Body.String())
	}
	assertStored("third.test", "")
	if w := env.do("DELETE", path, "", writer); w.Code != 200 {
		t.Fatalf("delete: %d", w.Code)
	}
	if w := env.do("DELETE", path, "", writer); w.Code != 404 {
		t.Fatalf("missing delete: %d", w.Code)
	}
}

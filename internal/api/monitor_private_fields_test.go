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

func TestMonitorPrivateFieldsCreateReadUpdateAndClone(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) {
		c.Auth.APIKeys = append(c.Auth.APIKeys, config.APIKeyConfig{Name: "editor", Hash: config.HashAPIKey("editor"), Permissions: []string{"monitors.read", "monitors.create", "monitors.update", "monitors.clone"}})
	}, false, nil)
	editor := map[string]string{"X-API-Key": "editor"}
	for _, tc := range []struct{ kind, target, settings string }{
		{"http", "https://private-user:private-pass@example.test/private-path?token=private-query#private-fragment", `{"method":"POST","body":"private-body","headers":{"Authorization":"private-header"}}`},
		{"http_multi", "https://example.test/private-base", `{"steps":[{"name":"login","url":"https://example.test/private-login?token=private-query","method":"POST","body":"private-body"}]}`},
		{"command", "command", `{"command":"/usr/bin/check","args":["private-argument"]}`},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			// Host-local monitors are written only by a super admin; the
			// scoped editor still reads them and must never see private fields.
			writer := editor
			if tc.kind == "command" {
				writer = admin
			}
			request := map[string]any{"name": tc.kind, "type": tc.kind, "target": tc.target, "interval": 60, "timeout": 10, "settings": json.RawMessage(tc.settings)}
			body, _ := json.Marshal(request)
			w := env.do("POST", "/api/v1/monitors", string(body), writer)
			var created storage.Monitor
			if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || w.Code != 201 || strings.Contains(w.Body.String(), "private-") {
				t.Fatalf("create response leaked or failed: %d %s", w.Code, w.Body)
			}
			path := fmt.Sprintf("/api/v1/monitors/%d", created.ID)
			w = env.do("GET", path, "", editor)
			if w.Code != 200 || strings.Contains(w.Body.String(), "private-") {
				t.Fatalf("read response leaked: %d %s", w.Code, w.Body)
			}
			var edit map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &edit); err != nil {
				t.Fatal(err)
			}
			edit["name"] = "renamed " + tc.kind
			body, _ = json.Marshal(edit)
			w = env.do("PUT", path, string(body), writer)
			if w.Code != 200 || strings.Contains(w.Body.String(), "private-") {
				t.Fatalf("redacted read/edit failed or leaked: %d %s", w.Code, w.Body)
			}
			stored, err := env.store.GetMonitor(context.Background(), created.ID)
			if err != nil || stored.Target != tc.target || !strings.Contains(string(stored.Settings), "private-") {
				t.Fatalf("redacted edit lost private configuration: %v", err)
			}
			w = env.do("POST", path+"/clone", "", writer)
			if w.Code != 201 || strings.Contains(w.Body.String(), "private-") {
				t.Fatalf("clone failed or disclosed copied secrets: %d %s", w.Code, w.Body)
			}
			var clone storage.Monitor
			if err := json.Unmarshal(w.Body.Bytes(), &clone); err != nil {
				t.Fatal(err)
			}
			copied, err := env.store.GetMonitor(context.Background(), clone.ID)
			if err != nil || copied.Target != tc.target || !strings.Contains(string(copied.Settings), "private-") || copied.Enabled {
				t.Fatalf("clone did not preserve private fields internally while paused: %v", err)
			}
		})
	}
	w := env.do("GET", "/api/v1/monitors", "", editor)
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-") {
		t.Fatalf("list exposed saved private fields: %d %s", w.Code, w.Body)
	}
}

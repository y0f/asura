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

func TestMonitorSecretReadAndUpdate(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) {
		c.Auth.APIKeys = append(c.Auth.APIKeys,
			config.APIKeyConfig{Name: "reader", Hash: config.HashAPIKey("reader"), Permissions: []string{"monitors.read"}},
			config.APIKeyConfig{Name: "writer", Hash: config.HashAPIKey("writer"), Permissions: []string{"monitors.update"}})
	}, false, nil)
	ctx := context.Background()
	monitor := &storage.Monitor{Name: "private monitor", Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10, Enabled: true, FailureThreshold: 1, SuccessThreshold: 1,
		Settings: json.RawMessage(`{"auth_method":"bearer","bearer_token":"private-bearer","headers":{"Arbitrary-Name":"private-header"}}`)}
	if err := env.store.CreateMonitor(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/monitors/%d", monitor.ID)
	reader := map[string]string{"X-API-Key": "reader"}
	var editable storage.Monitor
	for _, endpoint := range []string{"/api/v1/monitors", path} {
		w := env.do("GET", endpoint, "", reader)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "private-bearer") || strings.Contains(w.Body.String(), "private-header") {
			t.Fatalf("reader response leaked credentials: %d %s", w.Code, w.Body.String())
		}
		if endpoint == path {
			if err := json.Unmarshal(w.Body.Bytes(), &editable); err != nil {
				t.Fatal(err)
			}
		}
	}
	editable.Name = "updated name"
	editable.Target = "https://different.example.com"
	retargeted, _ := json.Marshal(editable)
	denied := env.do("PUT", path, string(retargeted), map[string]string{"X-API-Key": "writer"})
	if denied.Code != http.StatusBadRequest || !strings.Contains(denied.Body.String(), "credential destination") {
		t.Fatalf("retargeting hidden credentials was accepted: %d %s", denied.Code, denied.Body.String())
	}
	unchanged, err := env.store.GetMonitor(ctx, monitor.ID)
	if err != nil || unchanged.Target != monitor.Target || !strings.Contains(string(unchanged.Settings), "private-header") {
		t.Fatal("denied retarget changed stored monitor or credentials")
	}
	editable.Target = monitor.Target
	body, _ := json.Marshal(editable)
	w := env.do("PUT", path, string(body), map[string]string{"X-API-Key": "writer"})
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "updated name") || strings.Contains(w.Body.String(), "settings") {
		t.Fatalf("write-only update should return only its identifier: %d %s", w.Code, w.Body.String())
	}
	stored, err := env.store.GetMonitor(ctx, monitor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != editable.Name || !strings.Contains(string(stored.Settings), "private-bearer") || !strings.Contains(string(stored.Settings), "private-header") {
		t.Fatalf("redacted round trip lost credentials or edit: %+v", stored)
	}
	var clear map[string]any
	_ = json.Unmarshal(body, &clear)
	clear["target"] = "https://different.example.com"
	clear["clear_secrets"] = []string{"bearer_token", "headers.arbitrary-name"}
	body, _ = json.Marshal(clear)
	w = env.do("PUT", path, string(body), map[string]string{"X-API-Key": "writer"})
	if w.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", w.Code, w.Body.String())
	}
	stored, err = env.store.GetMonitor(ctx, monitor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored.Settings), "private-") {
		t.Fatalf("explicit clear retained credentials: %s", stored.Settings)
	}
	if stored.Target != "https://different.example.com" {
		t.Fatal("explicit clear did not permit the new destination")
	}
}

func TestNotificationSecretRoundTrip(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	ctx := context.Background()
	channel := &storage.NotificationChannel{Name: "hook", Type: "webhook", Enabled: true, Settings: json.RawMessage(`{"url":"https://example.com/private-webhook","secret":"private-signature"}`)}
	if err := env.store.CreateNotificationChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	w := env.do("GET", "/api/v1/notifications", "", admin)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "private-") {
		t.Fatalf("list leaked channel credentials: %d %s", w.Code, w.Body.String())
	}
	var list struct {
		Data []*storage.NotificationChannel `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 {
		t.Fatalf("channels = %d", len(list.Data))
	}
	list.Data[0].Name = "updated hook"
	body, _ := json.Marshal(list.Data[0])
	w = env.do("PUT", fmt.Sprintf("/api/v1/notifications/%d", channel.ID), string(body), admin)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "private-") {
		t.Fatalf("update leaked channel credentials: %d %s", w.Code, w.Body.String())
	}
	stored, err := env.store.GetNotificationChannel(ctx, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "updated hook" || !strings.Contains(string(stored.Settings), "private-webhook") || !strings.Contains(string(stored.Settings), "private-signature") {
		t.Fatalf("redacted update lost credentials: %+v", stored)
	}
}

func TestHeartbeatTokenIsRevealedOnlyOnCreate(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	w := env.do("POST", "/api/v1/monitors", `{"name":"heartbeat","type":"heartbeat","interval":60,"timeout":10}`, admin)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Monitor   storage.Monitor   `json:"monitor"`
		Heartbeat storage.Heartbeat `json:"heartbeat"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Heartbeat.Token == "" {
		t.Fatal("creation did not return the new heartbeat token")
	}
	stored, err := env.store.GetHeartbeatByMonitorID(context.Background(), created.Monitor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Token == created.Heartbeat.Token {
		t.Fatal("heartbeat token was stored in plaintext")
	}
	w = env.do("GET", fmt.Sprintf("/api/v1/monitors/%d", created.Monitor.ID), "", admin)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), created.Heartbeat.Token) || strings.Contains(w.Body.String(), stored.Token) {
		t.Fatalf("GET exposed a heartbeat credential or hash: %d %s", w.Code, w.Body.String())
	}
	if w := env.do("GET", "/api/v1/heartbeat/"+created.Heartbeat.Token, "", nil); w.Code != http.StatusOK {
		t.Fatalf("original token failed: %d", w.Code)
	}
	if w := env.do("GET", "/api/v1/heartbeat/"+stored.Token, "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("stored hash accepted as a bearer token: %d", w.Code)
	}
}

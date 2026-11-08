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

func TestNotificationOmittedFieldsAndCredentialRetargeting(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) {
		c.Auth.APIKeys = append(c.Auth.APIKeys, config.APIKeyConfig{Name: "writer", Hash: config.HashAPIKey("writer"), Permissions: []string{"notifications.update", "notifications.delete"}})
	}, false, nil)
	ctx := context.Background()
	channel := &storage.NotificationChannel{Name: "matrix", Type: "matrix", Enabled: false, Settings: json.RawMessage(`{"homeserver":"https://matrix.test","access_token":"saved-secret","room_id":"!room:test"}`), Events: []string{"incident.created"}}
	if err := env.store.CreateNotificationChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/notifications/%d", channel.ID)
	writer := map[string]string{"X-API-Key": "writer"}
	w := env.do("PUT", path, `{"name":"renamed"}`, writer)
	if w.Code != 200 || w.Body.String() != fmt.Sprintf("{\"id\":%d}\n", channel.ID) {
		t.Fatalf("write-only partial edit: %d %s", w.Code, w.Body.String())
	}
	got, err := env.store.GetNotificationChannel(ctx, channel.ID)
	if err != nil || got.Name != "renamed" || got.Enabled || len(got.Events) != 1 || !strings.Contains(string(got.Settings), "saved-secret") {
		t.Fatal("omitted fields lost their current values")
	}
	w = env.do("PUT", path, `{"settings":{"homeserver":"https://other.test","room_id":"!room:test"}}`, writer)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "credential destination") || strings.Contains(w.Body.String(), "saved-secret") {
		t.Fatalf("retarget accepted saved credential: %d %s", w.Code, w.Body.String())
	}
	got, err = env.store.GetNotificationChannel(ctx, channel.ID)
	if err != nil || strings.Contains(string(got.Settings), "other.test") {
		t.Fatal("denied edit changed destination")
	}
	w = env.do("PUT", path, `{"settings":{"homeserver":"https://other.test","room_id":"!room:test","access_token":"new-secret"},"events":null}`, writer)
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("replacement: %d %s", w.Code, w.Body.String())
	}
	got, err = env.store.GetNotificationChannel(ctx, channel.ID)
	if err != nil || len(got.Events) != 0 || !strings.Contains(string(got.Settings), "new-secret") {
		t.Fatal("replacement or explicit all-events selection was lost")
	}
	if w := env.do("DELETE", path, "", writer); w.Code != 200 {
		t.Fatalf("delete: %d", w.Code)
	}
	if w := env.do("DELETE", path, "", writer); w.Code != 404 {
		t.Fatalf("missing delete: %d", w.Code)
	}
}

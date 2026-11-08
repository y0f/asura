package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/storage"
)

func TestSettingsAmbiguityCannotDisappearDuringAPISecretMerge(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	ctx := context.Background()
	monitor := &storage.Monitor{Name: "original", Type: "http", Target: "https://example.test", Interval: 60, Timeout: 10, FailureThreshold: 1, SuccessThreshold: 1, Settings: json.RawMessage(`{"bearer_token":"private-original"}`)}
	if err := env.store.CreateMonitor(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	channel := &storage.NotificationChannel{Name: "original", Type: "telegram", Settings: json.RawMessage(`{"bot_token":"private-original","chat_id":"123"}`)}
	if err := env.store.CreateNotificationChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, body string }{
		{fmt.Sprintf("/api/v1/monitors/%d", monitor.ID), `{"name":"changed","type":"http","target":"https://example.test","interval":60,"timeout":10,"failure_threshold":1,"success_threshold":1,"settings":{"bearer_token":"","bearer_token":null}}`},
		{fmt.Sprintf("/api/v1/notifications/%d", channel.ID), `{"name":"changed","type":"telegram","settings":{"bot_token":"","bot_token":null,"chat_id":"123"}}`},
		{fmt.Sprintf("/api/v1/notifications/%d", channel.ID), `{"name":"changed","type":"telegram","settings":{"bot_token":42,"chat_id":"123"}}`},
	} {
		w := env.do("PUT", tc.path, tc.body, admin)
		if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "private-original") {
			t.Fatalf("ambiguous edit accepted or disclosed stored data: %d %s", w.Code, w.Body.String())
		}
	}
	gotMonitor, err := env.store.GetMonitor(ctx, monitor.ID)
	if err != nil || gotMonitor.Name != monitor.Name || string(gotMonitor.Settings) != string(monitor.Settings) {
		t.Fatalf("rejected edit changed monitor: %v", err)
	}
	gotChannel, err := env.store.GetNotificationChannel(ctx, channel.ID)
	if err != nil || gotChannel.Name != channel.Name || string(gotChannel.Settings) != string(channel.Settings) {
		t.Fatalf("rejected edit changed notification channel: %v", err)
	}
}

func TestOmittedSettingsPreserveUnambiguousLegacyCredentials(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	ctx := context.Background()
	monitor := &storage.Monitor{Name: "original", Type: "http", Target: "https://example.test", Interval: 60, Timeout: 10, FailureThreshold: 1, SuccessThreshold: 1, Settings: json.RawMessage(`{"BEARER_TOKEN":"private-original","HEADERS":{"X-Token":"private-header"}}`)}
	if err := env.store.CreateMonitor(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	channel := &storage.NotificationChannel{Name: "original", Type: "telegram", Settings: json.RawMessage(`{"BOT_TOKEN":"private-original","CHAT_ID":"123"}`)}
	if err := env.store.CreateNotificationChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, body string }{
		{fmt.Sprintf("/api/v1/monitors/%d", monitor.ID), `{"name":"changed","type":"http","target":"https://example.test","interval":60,"timeout":10,"failure_threshold":1,"success_threshold":1}`},
		{fmt.Sprintf("/api/v1/notifications/%d", channel.ID), `{"name":"changed","type":"telegram"}`},
	} {
		w := env.do("PUT", tc.path, tc.body, admin)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "private-") {
			t.Fatalf("omitted legacy settings were rejected or leaked: %d %s", w.Code, w.Body.String())
		}
	}
	gotMonitor, err := env.store.GetMonitor(ctx, monitor.ID)
	if err != nil || !strings.Contains(string(gotMonitor.Settings), `"bearer_token":"private-original"`) || !strings.Contains(string(gotMonitor.Settings), `"X-Token":"private-header"`) {
		t.Fatalf("omitted legacy monitor settings were lost: %v", err)
	}
	gotChannel, err := env.store.GetNotificationChannel(ctx, channel.ID)
	if err != nil || !strings.Contains(string(gotChannel.Settings), `"bot_token":"private-original"`) || !strings.Contains(string(gotChannel.Settings), `"chat_id":"123"`) {
		t.Fatalf("omitted legacy channel settings were lost: %v", err)
	}
}

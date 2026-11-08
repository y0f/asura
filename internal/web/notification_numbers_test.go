package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestNotificationTypedNumberDefaultsAndSelection(t *testing.T) {
	for _, tc := range []struct {
		kind, field string
		fallback    int
	}{
		{"email", "notif_email_port", 0}, {"ntfy", "notif_ntfy_priority", 0},
		{"pushover", "notif_pushover_priority", 0}, {"gotify", "notif_gotify_priority", 5},
	} {
		for _, value := range []struct {
			raw  string
			want int
		}{{"", tc.fallback}, {"0", 0}, {"7", 7}} {
			got, err := notificationFormNumber(buildFormRequest(url.Values{tc.field: {value.raw}}), tc.kind)
			if err != nil || got != value.want {
				t.Errorf("%s %q = %d,%v; want%d", tc.kind, value.raw, got, err, value.want)
			}
		}
	}
	h := testWebHandler(t)
	for _, form := range []url.Values{
		{"type": {"webhook"}, "notif_webhook_url": {"https://example.test"}, "notif_gotify_priority": {"bad"}},
		{"type": {"gotify"}, "notif_settings_mode": {"json"}, "settings_json": {`{"priority":7}`}, "notif_gotify_priority": {"bad"}},
	} {
		ch, err := h.parseNotificationForm(buildFormRequest(form))
		if err != nil || ch == nil {
			t.Fatalf("inactive typed value rejected: %v", err)
		}
		if form.Get("notif_settings_mode") == "json" && string(ch.Settings) != `{"priority":7}` {
			t.Fatal("JSON settings changed")
		}
	}
}

func TestNotificationInvalidNumbersNeverMutate(t *testing.T) {
	for _, tc := range []struct {
		kind, field string
		fields      url.Values
	}{
		{"email", "notif_email_port", url.Values{"notif_email_host": {"smtp.example.test"}, "notif_email_port": {"587"}, "notif_email_from": {"sender@example.test"}, "notif_email_to": {"recipient@example.test"}}},
		{"ntfy", "notif_ntfy_priority", url.Values{"notif_ntfy_topic": {"ops"}, "notif_ntfy_priority": {"3"}}},
		{"pushover", "notif_pushover_priority", url.Values{"notif_pushover_user_key": {"fixture-user"}, "notif_pushover_app_token": {"fixture-token"}, "notif_pushover_priority": {"1"}}},
		{"gotify", "notif_gotify_priority", url.Values{"notif_gotify_server_url": {"https://gotify.example.test"}, "notif_gotify_app_token": {"fixture-token"}, "notif_gotify_priority": {"7"}}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			h, store := storeHandler(t)
			form := tc.fields
			form.Set("type", tc.kind)
			form.Set("name", "Original")
			form.Set("event_mode", "all")
			form.Set("notif_settings_mode", "form")
			h.NotificationCreate(httptest.NewRecorder(), adminRequest("POST", "/notifications", form))
			before, err := store.ListNotificationChannels(context.Background())
			if err != nil || len(before) != 1 {
				t.Fatalf("valid baseline rejected: %v", err)
			}
			original := before[0]
			for _, invalid := range []string{"not-a-number", "1.5", "99999999999999999999999999999999"} {
				form.Set(tc.field, invalid)
				form.Set("name", "Changed")
				for _, update := range []bool{false, true} {
					w := httptest.NewRecorder()
					r := adminRequest("POST", "/notifications", form)
					if update {
						r.SetPathValue("id", strconvI(original.ID))
						h.NotificationUpdate(w, r)
					} else {
						h.NotificationCreate(w, r)
					}
					var message string
					for _, cookie := range w.Result().Cookies() {
						if cookie.Name == "toast" {
							message, _ = url.QueryUnescape(cookie.Value)
						}
					}
					if w.Code != http.StatusSeeOther || !strings.HasPrefix(message, "error:") || !strings.Contains(message, "whole number") || strings.Contains(message, invalid) {
						t.Fatalf("invalid numeric input lacked a safe error: status%d %q", w.Code, message)
					}
					got, err := store.ListNotificationChannels(context.Background())
					if err != nil || len(got) != 1 || got[0].Name != original.Name || string(got[0].Settings) != string(original.Settings) {
						t.Fatalf("invalid %s mutated configuration: %v", invalid, err)
					}
					if !json.Valid(got[0].Settings) {
						t.Fatal("stored settings invalid")
					}
				}
			}
		})
	}
}

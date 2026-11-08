package web

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/y0f/asura/internal/notifier"
)

func TestAssembleNotificationSettingsWebhook(t *testing.T) {
	form := url.Values{
		"notif_webhook_url":    {"https://example.com/hook"},
		"notif_webhook_secret": {"s3cret"},
	}
	r := buildFormRequest(form)
	raw, err := assembleNotificationSettings(r, "webhook")
	if err != nil {
		t.Fatal(err)
	}

	var s notifier.WebhookSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.URL != "https://example.com/hook" {
		t.Errorf("url = %q", s.URL)
	}
	if s.Secret != "s3cret" {
		t.Errorf("secret = %q", s.Secret)
	}
}

func TestAssembleGotifyPriority(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
	}{{"", 5}, {"0", 0}, {"7", 7}} {
		t.Run(tc.value, func(t *testing.T) {
			raw, err := assembleNotificationSettings(buildFormRequest(url.Values{"notif_gotify_priority": {tc.value}}), "gotify")
			if err != nil {
				t.Fatal(err)
			}
			var settings struct {
				Priority *int `json:"priority"`
			}
			if err := json.Unmarshal(raw, &settings); err != nil {
				t.Fatal(err)
			}
			if settings.Priority == nil || *settings.Priority != tc.want {
				t.Fatalf("priority not preserved in %s", raw)
			}
		})
	}
}

func TestAssembleNotificationSettingsTelegram(t *testing.T) {
	form := url.Values{
		"notif_telegram_bot_token": {"123:ABC"},
		"notif_telegram_chat_id":   {"-100123"},
	}
	r := buildFormRequest(form)
	raw, err := assembleNotificationSettings(r, "telegram")
	if err != nil {
		t.Fatal(err)
	}

	var s notifier.TelegramSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.BotToken != "123:ABC" || s.ChatID != "-100123" {
		t.Errorf("telegram = %+v", s)
	}
}

func TestAssembleNotificationSettingsDiscord(t *testing.T) {
	form := url.Values{
		"notif_discord_webhook_url": {"https://discord.com/api/webhooks/123"},
	}
	r := buildFormRequest(form)
	raw, err := assembleNotificationSettings(r, "discord")
	if err != nil {
		t.Fatal(err)
	}

	var s notifier.DiscordSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.WebhookURL != "https://discord.com/api/webhooks/123" {
		t.Errorf("discord webhook = %q", s.WebhookURL)
	}
}

func TestAssembleNotificationSettingsSlack(t *testing.T) {
	form := url.Values{
		"notif_slack_webhook_url": {"https://hooks.slack.com/services/T/B/X"},
		"notif_slack_channel":     {"#alerts"},
	}
	r := buildFormRequest(form)
	raw, err := assembleNotificationSettings(r, "slack")
	if err != nil {
		t.Fatal(err)
	}

	var s notifier.SlackSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.WebhookURL != "https://hooks.slack.com/services/T/B/X" {
		t.Errorf("slack webhook = %q", s.WebhookURL)
	}
	if s.Channel != "#alerts" {
		t.Errorf("channel = %q", s.Channel)
	}
}

func TestAssembleNotificationSettingsEmail(t *testing.T) {
	form := url.Values{
		"notif_email_host":     {"smtp.example.com"},
		"notif_email_port":     {"587"},
		"notif_email_username": {"user"},
		"notif_email_password": {"pass"},
		"notif_email_from":     {"alerts@example.com"},
		"notif_email_to":       {"a@b.com, c@d.com"},
	}
	r := buildFormRequest(form)
	raw, err := assembleNotificationSettings(r, "email")
	if err != nil {
		t.Fatal(err)
	}

	var s notifier.EmailSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.Host != "smtp.example.com" {
		t.Errorf("host = %q", s.Host)
	}
	if s.Port != 587 {
		t.Errorf("port = %d", s.Port)
	}
	if s.Username != "user" || s.Password != "pass" {
		t.Errorf("credentials = %q / %q", s.Username, s.Password)
	}
	if s.From != "alerts@example.com" {
		t.Errorf("from = %q", s.From)
	}
	if len(s.To) != 2 || s.To[0] != "a@b.com" || s.To[1] != "c@d.com" {
		t.Errorf("to = %v", s.To)
	}
}

func TestAssembleNotificationSettingsUnknown(t *testing.T) {
	r := buildFormRequest(url.Values{})
	raw, err := assembleNotificationSettings(r, "unknown")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{}" {
		t.Errorf("expected empty object for unknown type, got %s", raw)
	}
}

func TestParseNotificationEventsCheckboxes(t *testing.T) {
	form := url.Values{
		"event_incident_created":  {"on"},
		"event_incident_resolved": {"on"},
	}
	r := buildFormRequest(form)
	events := parseNotificationEvents(r)

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %v", len(events), events)
	}
	has := map[string]bool{}
	for _, e := range events {
		has[e] = true
	}
	if !has["incident.created"] || !has["incident.resolved"] {
		t.Errorf("events = %v", events)
	}
}

func TestParseNotificationEventsAllChecked(t *testing.T) {
	form := url.Values{
		"event_incident_created":      {"on"},
		"event_incident_resolved":     {"on"},
		"event_incident_acknowledged": {"on"},
		"event_content_changed":       {"on"},
	}
	r := buildFormRequest(form)
	events := parseNotificationEvents(r)

	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}
}

func TestParseNotificationEventsCSVFallback(t *testing.T) {
	form := url.Values{
		"events": {"incident.created,content.changed"},
	}
	r := buildFormRequest(form)
	events := parseNotificationEvents(r)

	if len(events) != 2 {
		t.Fatalf("expected 2 events from CSV, got %d: %v", len(events), events)
	}
}

func TestParseNotificationEventsEmpty(t *testing.T) {
	r := buildFormRequest(url.Values{})
	events := parseNotificationEvents(r)

	if len(events) != 0 {
		t.Errorf("expected empty events, got %v", events)
	}
}

func TestParseNotificationFormFormMode(t *testing.T) {
	form := url.Values{
		"name":                    {"My Webhook"},
		"type":                    {"webhook"},
		"enabled":                 {"on"},
		"notif_settings_mode":     {"form"},
		"notif_webhook_url":       {"https://example.com/hook"},
		"event_incident_created":  {"on"},
		"event_incident_resolved": {"on"},
	}

	h := testWebHandler(t)
	r := buildFormRequest(form)
	ch, err := h.parseNotificationForm(r)
	if err != nil {
		t.Fatal(err)
	}

	if ch.Name != "My Webhook" {
		t.Errorf("Name = %q", ch.Name)
	}
	if ch.Type != "webhook" {
		t.Errorf("Type = %q", ch.Type)
	}
	if !ch.Enabled {
		t.Error("Enabled should be true")
	}
	if len(ch.Events) != 2 {
		t.Errorf("Events = %v", ch.Events)
	}

	var s notifier.WebhookSettings
	if err := json.Unmarshal(ch.Settings, &s); err != nil {
		t.Fatal(err)
	}
	if s.URL != "https://example.com/hook" {
		t.Errorf("webhook URL = %q", s.URL)
	}
}

func TestParseNotificationFormJSONMode(t *testing.T) {
	form := url.Values{
		"name":                  {"JSON Channel"},
		"type":                  {"webhook"},
		"enabled":               {"on"},
		"notif_settings_mode":   {"json"},
		"settings_json":         {`{"url":"https://example.com","secret":"abc"}`},
		"event_content_changed": {"on"},
	}

	h := testWebHandler(t)
	r := buildFormRequest(form)
	ch, err := h.parseNotificationForm(r)
	if err != nil {
		t.Fatal(err)
	}

	var s notifier.WebhookSettings
	if err := json.Unmarshal(ch.Settings, &s); err != nil {
		t.Fatal(err)
	}
	if s.URL != "https://example.com" || s.Secret != "abc" {
		t.Errorf("json mode settings = %+v", s)
	}
}

func TestParseNotificationFormJSONModeInvalid(t *testing.T) {
	form := url.Values{
		"name":                {"Bad"},
		"type":                {"webhook"},
		"notif_settings_mode": {"json"},
		"settings_json":       {`{not valid`},
	}

	h := testWebHandler(t)
	r := buildFormRequest(form)
	ch, err := h.parseNotificationForm(r)
	if err != nil {
		t.Fatal(err)
	}

	if ch.Settings != nil {
		t.Errorf("invalid JSON should be nil, got %s", ch.Settings)
	}
}

package views

import (
	"encoding/json"

	"github.com/y0f/asura/v2/internal/redact"
	"github.com/y0f/asura/v2/internal/storage"
)

// Stored credentials are never written back into HTML. Edit forms render
// secret fields empty (with a "saved" placeholder) and the handlers keep the
// stored value when the field comes back blank. These tables list which
// settings keys are secrets, per monitor type and per notification type.

// These aliases keep the form contract on the shared API redaction policy.
var MonitorSecretKeys = redact.MonitorSecretKeys
var NotificationSecretKeys = redact.NotificationSecretKeys

// NotificationRequiredSecrets lists the secret keys a channel cannot work
// without. They can be replaced but not removed.
var NotificationRequiredSecrets = map[string][]string{
	"webhook":    {"url"},
	"telegram":   {"bot_token"},
	"discord":    {"webhook_url"},
	"slack":      {"webhook_url"},
	"teams":      {"webhook_url"},
	"googlechat": {"webhook_url"},
	"pagerduty":  {"routing_key"},
	"opsgenie":   {"api_key"},
	"pushover":   {"user_key", "app_token"},
	"matrix":     {"access_token"},
	"gotify":     {"app_token"},
}

// MissingRequiredSecret returns the first required secret key that is empty in
// the channel's settings, or "" when all are present.
func MissingRequiredSecret(ch *storage.NotificationChannel) string {
	var m map[string]any
	if err := json.Unmarshal(ch.Settings, &m); err != nil {
		return ""
	}
	for _, k := range NotificationRequiredSecrets[ch.Type] {
		if v, _ := m[k].(string); v == "" {
			return k
		}
	}
	return ""
}

func RedactSettings(raw json.RawMessage, keys []string) (json.RawMessage, map[string]bool) {
	return redact.RedactSettings(raw, keys)
}

func MergeSecrets(updated, old json.RawMessage, keys []string, clear map[string]bool) (json.RawMessage, error) {
	return redact.MergeSecrets(updated, old, keys, clear)
}

// ClearSet reads the "Remove saved value" checkboxes from a submitted form.
func ClearSet(values []string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}

// channelEditData is what the notifications page hands to the edit dialog:
// the channel with its secrets blanked, plus which secrets are stored.
type channelEditData struct {
	*storage.NotificationChannel
	SecretsSet []string `json:"secrets_set"`
}

// ChannelEditJSON returns the channel as JSON for the edit dialog, without
// secret values.
func ChannelEditJSON(ch *storage.NotificationChannel) string {
	c := *ch
	_, set := RedactSettings(ch.Settings, NotificationSecretKeys[ch.Type])
	c.Settings = redact.Notification(ch).Settings
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	return ToJSON(channelEditData{NotificationChannel: &c, SecretsSet: keys})
}

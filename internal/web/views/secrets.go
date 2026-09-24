package views

import (
	"encoding/json"

	"github.com/y0f/asura/internal/storage"
)

// Stored credentials are never written back into HTML. Edit forms render
// secret fields empty (with a "saved" placeholder) and the handlers keep the
// stored value when the field comes back blank. These tables list which
// settings keys are secrets, per monitor type and per notification type.

// MonitorSecretKeys lists the secret settings keys for each monitor type.
var MonitorSecretKeys = map[string][]string{
	"http":  {"basic_auth_pass", "bearer_token", "oauth2_client_secret", "mtls_client_key"},
	"mqtt":  {"password"},
	"redis": {"password"},
}

// NotificationSecretKeys lists the secret settings keys for each channel type.
var NotificationSecretKeys = map[string][]string{
	"webhook":    {"secret"},
	"telegram":   {"bot_token"},
	"discord":    {"webhook_url"},
	"slack":      {"webhook_url"},
	"email":      {"password"},
	"teams":      {"webhook_url"},
	"pagerduty":  {"routing_key"},
	"opsgenie":   {"api_key"},
	"pushover":   {"user_key", "app_token"},
	"googlechat": {"webhook_url"},
	"matrix":     {"access_token"},
	"gotify":     {"app_token"},
}

// RedactSettings blanks the given keys in a settings JSON object. It returns the
// redacted JSON and the set of keys that held a value.
func RedactSettings(raw json.RawMessage, keys []string) (json.RawMessage, map[string]bool) {
	set := map[string]bool{}
	if len(raw) == 0 || len(keys) == 0 {
		return raw, set
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw, set
	}
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			set[k] = true
			m[k] = ""
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw, set
	}
	return out, set
}

// MergeSecrets copies each secret key from old into updated when updated
// leaves it empty, so a blank secret field means "keep the stored value".
func MergeSecrets(updated, old json.RawMessage, keys []string) json.RawMessage {
	if len(old) == 0 || len(keys) == 0 {
		return updated
	}
	var prev map[string]any
	if err := json.Unmarshal(old, &prev); err != nil {
		return updated
	}
	next := map[string]any{}
	if len(updated) > 0 {
		if err := json.Unmarshal(updated, &next); err != nil {
			return updated
		}
	}
	changed := false
	for _, k := range keys {
		pv, _ := prev[k].(string)
		nv, _ := next[k].(string)
		if nv == "" && pv != "" {
			next[k] = pv
			changed = true
		}
	}
	if !changed {
		return updated
	}
	out, err := json.Marshal(next)
	if err != nil {
		return updated
	}
	return out
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
	redacted, set := RedactSettings(ch.Settings, NotificationSecretKeys[ch.Type])
	c.Settings = redacted
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	return ToJSON(channelEditData{NotificationChannel: &c, SecretsSet: keys})
}

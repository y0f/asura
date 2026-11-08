package redact

import "encoding/json"

// MonitorSecretKeys lists the secret settings keys for each monitor type.
var MonitorSecretKeys = map[string][]string{
	"http":  {"basic_auth_pass", "bearer_token", "oauth2_client_secret", "mtls_client_key"},
	"mqtt":  {"password"},
	"redis": {"password"},
}

// NotificationSecretKeys lists the secret settings keys for each channel type.
var NotificationSecretKeys = map[string][]string{
	"webhook":    {"url", "secret"},
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
	if len(raw) == 0 {
		return json.RawMessage(`{}`), set
	}
	m, err := settingsObject(raw, true)
	if err != nil {
		return json.RawMessage(`{}`), set
	}
	redactFields(m, keys, set)
	out, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(`{}`), set
	}
	return out, set
}

func redactFields(object map[string]any, keys []string, stored map[string]bool) {
	for _, key := range keys {
		if value, ok := object[key]; ok && value != nil && value != "" {
			stored[key] = true
			object[key] = ""
		}
	}
}

// MergeSecrets copies each secret key from old into updated when updated
// leaves it empty, so a blank secret field means "keep the stored value".
// Keys listed in clear are left empty, which is how a stored secret is removed.
// Omitted settings preserve the entire stored object and normalize unambiguous
// legacy field spelling. Explicit submissions retain strict field validation.
func MergeSecrets(updated, old json.RawMessage, keys []string, clear map[string]bool) (json.RawMessage, error) {
	inherited := len(updated) == 0
	if inherited {
		updated = old
	}
	next, err := settingsObject(updated, inherited)
	if err != nil {
		return nil, err
	}
	prev, err := settingsObject(old, true)
	if err != nil {
		return nil, err
	}
	changed := inherited
	for _, k := range keys {
		for _, object := range []map[string]any{prev, next} {
			if value := object[k]; value != nil {
				if _, ok := value.(string); !ok {
					return nil, errAmbiguousSettings
				}
			}
		}
		if clear[k] {
			next[k] = ""
			changed = true
			continue
		}
		pv, _ := prev[k].(string)
		nv, _ := next[k].(string)
		if nv == "" && pv != "" {
			next[k] = pv
			changed = true
		}
	}
	if !changed {
		return updated, nil
	}
	out, err := json.Marshal(next)
	if err != nil {
		return nil, err
	}
	return out, nil
}

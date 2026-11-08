package redact

import (
	"encoding/json"
	"errors"
	"reflect"
)

// Notification routing includes the credential's server, account and recipients.
// Cosmetic settings such as priority or sound do not change its destination.
var notificationDestinations = map[string][]string{
	"webhook":  {"url"},
	"telegram": {"chat_id"},
	"slack":    {"webhook_url", "channel"},
	"email":    {"host", "port", "username", "tls_mode", "from", "to", "cc", "bcc"},
	"opsgenie": {"region"},
	"pushover": {"user_key", "device"},
	"matrix":   {"homeserver", "room_id"},
	"gotify":   {"server_url"},
}

// MergeNotificationSecrets merges a redacted edit and prevents an ordinary
// writer from reusing hidden credentials with a different destination.
func MergeNotificationSecrets(kind string, submitted, old json.RawMessage, clear map[string]bool, canRetarget bool) (json.RawMessage, error) {
	keys := NotificationSecretKeys[kind]
	merged, err := MergeSecrets(submitted, old, keys, clear)
	if err != nil || canRetarget || len(notificationDestinations[kind]) == 0 {
		return merged, err
	}
	previous, err := settingsObject(old, true)
	if err != nil {
		return nil, err
	}
	next, err := settingsObject(merged, false)
	if err != nil {
		return nil, err
	}
	changed := false
	for _, field := range notificationDestinations[kind] {
		if !reflect.DeepEqual(previous[field], next[field]) {
			changed = true
			break
		}
	}
	if !changed {
		return merged, nil
	}
	input, err := settingsObject(submitted, false)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		stored, _ := previous[key].(string)
		replacement, _ := input[key].(string)
		if stored != "" && replacement == "" && !clear[key] {
			return nil, errors.New("changing the credential destination requires replacing or clearing saved credentials")
		}
	}
	return merged, nil
}

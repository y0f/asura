package redact

import (
	"encoding/json"
	"strconv"

	"github.com/y0f/asura/internal/storage"
)

var (
	allMonitorSecretKeys      = allSecretKeys(MonitorSecretKeys)
	allNotificationSecretKeys = allSecretKeys(NotificationSecretKeys)
)

func allSecretKeys(registry map[string][]string) []string {
	seen := make(map[string]bool)
	var keys []string
	for _, fields := range registry {
		for _, key := range fields {
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	return keys
}

// MonitorSettings hides credentials even in stale settings left from another
// monitor type. Custom header names are unrestricted, so every value is private.
func MonitorSettings(raw json.RawMessage) (json.RawMessage, map[string]bool) {
	return monitorSettings(raw, false)
}

func monitorSettings(raw json.RawMessage, diagnostics bool) (json.RawMessage, map[string]bool) {
	stored := make(map[string]bool)
	object, err := settingsObject(raw, true)
	if err != nil {
		return json.RawMessage(`{}`), stored
	}
	if err := checkMonitorPrivateContainers(object); err != nil {
		return json.RawMessage(`{}`), stored
	}
	redactFields(object, allMonitorSecretKeys, stored)
	redactFields(object, monitorPayloadKeys, stored)
	if args, exists := object["args"]; exists && args != nil {
		if values, ok := args.([]any); !ok || len(values) != 0 {
			stored["args"] = true
		}
		object["args"] = []any{}
	}
	redactURL := func(object map[string]any, key, path string) {
		if value, exists := object[key]; exists {
			url, _ := value.(string)
			safe := URLOrigin(url)
			if safe != url || value != nil && url == "" && value != "" {
				stored[path] = true
			}
			object[key] = safe
		}
	}
	redactURL(object, "oauth2_token_url", "oauth2_token_url")
	redactHeaders := func(object map[string]any, prefix string) {
		if headers, ok := object["headers"].(map[string]any); ok {
			for name, value := range headers {
				if value != nil && value != "" {
					stored[prefix+"headers."+name] = true
					headers[name] = ""
				}
			}
		}
	}
	redactHeaders(object, "")
	if steps, ok := object["steps"].([]any); ok {
		for i, step := range steps {
			if object, ok := step.(map[string]any); ok {
				prefix := "steps." + strconv.Itoa(i) + "."
				redactHeaders(object, prefix)
				redactURL(object, "url", prefix+"url")
				if body, exists := object["body"]; exists && body != nil && body != "" {
					stored[prefix+"body"] = true
					object["body"] = ""
				}
				for _, key := range []string{"extract_regex", "extract_json"} {
					if value := object[key]; value != nil && value != "" {
						stored[prefix+key] = true
						if !diagnostics {
							object[key] = ""
						}
					}
				}
			}
		}
	}
	clean, err := json.Marshal(object)
	if err != nil {
		return json.RawMessage(`{}`), stored
	}
	return clean, stored
}

func Monitor(m *storage.Monitor) *storage.Monitor {
	return MonitorWithDiagnostics(m, false)
}

// MonitorWithDiagnostics exposes assertion definitions only with a separately
// authorized diagnostic read. Credentials remain hidden even with that grant.
func MonitorWithDiagnostics(m *storage.Monitor, diagnostics bool) *storage.Monitor {
	if m == nil {
		return nil
	}
	copy := *m
	copy.Assertions = nil
	if diagnostics {
		copy.Assertions = append(json.RawMessage(nil), m.Assertions...)
	}
	copy.Target = MonitorTarget(m.Type, m.Target)
	if len(m.Settings) > 0 {
		copy.Settings, _ = monitorSettings(m.Settings, diagnostics)
	}
	return &copy
}

// MonitorStoredSecrets exposes presence only, for safe retained-value hints.
func MonitorStoredSecrets(m *storage.Monitor) map[string]bool {
	_, stored := MonitorSettings(m.Settings)
	if m.Target != "" && MonitorTarget(m.Type, m.Target) != m.Target {
		stored["target"] = true
	}
	if present, err := hasAssertions(m.Assertions); present || err != nil {
		stored["assertions"] = true
	}
	return stored
}

// MonitorSummaries avoids decoding settings that collection views do not use.
// It also prevents a future list template from accidentally rendering them.
func MonitorSummaries(monitors []*storage.Monitor) []*storage.Monitor {
	result := make([]*storage.Monitor, len(monitors))
	for i, monitor := range monitors {
		if monitor == nil {
			continue
		}
		copy := *monitor
		copy.Target = MonitorTarget(monitor.Type, monitor.Target)
		copy.Settings, copy.Assertions = nil, nil
		result[i] = &copy
	}
	return result
}

func Notification(ch *storage.NotificationChannel) *storage.NotificationChannel {
	if ch == nil {
		return nil
	}
	copy := *ch
	copy.Settings, _ = RedactSettings(ch.Settings, allNotificationSecretKeys)
	return &copy
}

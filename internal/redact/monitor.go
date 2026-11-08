package redact

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

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
	stored := make(map[string]bool)
	object, err := settingsObject(raw, true)
	if err != nil {
		return json.RawMessage(`{}`), stored
	}
	redactFields(object, allMonitorSecretKeys, stored)
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
				redactHeaders(object, "steps."+strconv.Itoa(i)+".")
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
	if m == nil {
		return nil
	}
	copy := *m
	if len(m.Settings) > 0 {
		copy.Settings, _ = MonitorSettings(m.Settings)
	}
	return &copy
}

func Notification(ch *storage.NotificationChannel) *storage.NotificationChannel {
	if ch == nil {
		return nil
	}
	copy := *ch
	copy.Settings, _ = RedactSettings(ch.Settings, allNotificationSecretKeys)
	return &copy
}

type stepIdentity struct{ name, url string }

func stepKey(step map[string]any) stepIdentity {
	name, _ := step["name"].(string)
	url, _ := step["url"].(string)
	return stepIdentity{name, url}
}

// MergeMonitorHeaders preserves blank, still-present header values. Removing a
// header entry removes its stored value; clear paths explicitly retain an empty
// value. Multi-step checks match an unambiguous name and URL across reordering.
func MergeMonitorHeaders(updated, old json.RawMessage, clear map[string]bool) (json.RawMessage, error) {
	next, err := settingsObject(updated, false)
	if err != nil {
		return nil, err
	}
	prev, err := settingsObject(old, true)
	if err != nil {
		return nil, err
	}
	clearHeaders := make(map[string]bool, len(clear))
	for path, remove := range clear {
		clearHeaders[strings.ToLower(path)] = remove
	}
	changed := false
	merge := func(next, prev map[string]any, prefix string) {
		nextHeaders, _ := next["headers"].(map[string]any)
		prevHeaders, _ := prev["headers"].(map[string]any)
		type headerValue struct {
			value     any
			ambiguous bool
		}
		folded := make(map[string]headerValue, len(prevHeaders))
		for name, value := range prevHeaders {
			key := strings.ToLower(name)
			_, exists := folded[key]
			folded[key] = headerValue{value, exists}
		}
		for name, value := range nextHeaders {
			if clearHeaders[strings.ToLower(prefix+"headers."+name)] {
				nextHeaders[name] = ""
				changed = true
				continue
			}
			if value != "" {
				continue
			}
			if previous, exists := prevHeaders[name]; exists {
				nextHeaders[name] = previous
				changed = true
				continue
			}
			if previous, exists := folded[strings.ToLower(name)]; exists && !previous.ambiguous {
				nextHeaders[name] = previous.value
				changed = true
			}
		}
	}
	merge(next, prev, "")
	type stepMatch struct {
		step  map[string]any
		count int
	}

	prevSteps, _ := prev["steps"].([]any)
	matches := make(map[stepIdentity]stepMatch, len(prevSteps))
	for _, previous := range prevSteps {
		if step, ok := previous.(map[string]any); ok {
			key := stepKey(step)
			match := matches[key]
			match.step, match.count = step, match.count+1
			matches[key] = match
		}
	}
	nextSteps, _ := next["steps"].([]any)
	for i, step := range nextSteps {
		nextStep, ok := step.(map[string]any)
		if !ok {
			continue
		}
		match := matches[stepKey(nextStep)]
		prefix := "steps." + strconv.Itoa(i) + "."
		if match.count > 1 {
			if headers, ok := nextStep["headers"].(map[string]any); ok {
				for name, value := range headers {
					if value == "" && !clearHeaders[strings.ToLower(prefix+"headers."+name)] {
						return updated, fmt.Errorf("step %d has an ambiguous saved identity; provide replacement header values or clear them explicitly", i+1)
					}
				}
			}
		}
		if match.count == 1 {
			merge(nextStep, match.step, prefix)
		}
		if match.count > 1 {
			merge(nextStep, nil, prefix)
		}
	}
	if !changed {
		return updated, nil
	}
	merged, err := json.Marshal(next)
	if err != nil {
		return updated, err
	}
	return merged, nil
}

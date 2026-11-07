package redact

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/y0f/asura/internal/storage"
)

// CheckMonitorDestination prevents an ordinary edit from sending hidden stored
// credentials to a new recipient. Call it on submitted settings before merging.
func CheckMonitorDestination(next, old *storage.Monitor, keys []string, clear map[string]bool) error {
	if next.Type != old.Type {
		return nil
	}
	var previous, submitted map[string]any
	_ = json.Unmarshal(old.Settings, &previous)
	_ = json.Unmarshal(next.Settings, &submitted)
	changed := next.Target != old.Target || !sameID(next.ProxyID, old.ProxyID)
	if len(next.Settings) > 0 {
		oldURL, _ := previous["oauth2_token_url"].(string)
		newURL, _ := submitted["oauth2_token_url"].(string)
		changed = changed || oldURL != newURL
	}
	if !changed {
		return nil
	}
	failure := func() error {
		return errors.New("changing the credential destination requires replacing or clearing saved credentials")
	}
	for _, key := range keys {
		stored, _ := previous[key].(string)
		replacement, _ := submitted[key].(string)
		if stored != "" && replacement == "" && !clear[key] {
			return failure()
		}
	}
	clearedHeaders := make(map[string]bool, len(clear))
	for path, remove := range clear {
		clearedHeaders[strings.ToLower(path)] = remove
	}
	retainedHeaders := func(previous, submitted map[string]any, prefix string, omitted bool) bool {
		oldHeaders, _ := previous["headers"].(map[string]any)
		newHeaders, _ := submitted["headers"].(map[string]any)
		private := make(map[string]bool, len(oldHeaders))
		for name, value := range oldHeaders {
			if value != nil && value != "" {
				private[strings.ToLower(name)] = true
			}
		}
		if omitted {
			for name := range private {
				if !clearedHeaders[prefix+"headers."+name] {
					return true
				}
			}
		}
		for name, value := range newHeaders {
			name = strings.ToLower(name)
			if value == "" && private[name] && !clearedHeaders[prefix+"headers."+name] {
				return true
			}
		}
		return false
	}
	omitted := len(next.Settings) == 0
	if retainedHeaders(previous, submitted, "", omitted) {
		return failure()
	}
	oldSteps, _ := previous["steps"].([]any)
	oldByIdentity := make(map[stepIdentity]map[string]any, len(oldSteps))
	for _, entry := range oldSteps {
		if step, ok := entry.(map[string]any); ok {
			oldByIdentity[stepKey(step)] = step
		}
	}
	newSteps, _ := submitted["steps"].([]any)
	if omitted {
		newSteps = oldSteps
	}
	for i, entry := range newSteps {
		if step, ok := entry.(map[string]any); ok {
			if retainedHeaders(oldByIdentity[stepKey(step)], step, "steps."+strconv.Itoa(i)+".", omitted) {
				return failure()
			}
		}
	}
	return nil
}

func sameID(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

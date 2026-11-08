package redact

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/y0f/asura/internal/storage"
)

// Payloads can carry arbitrary credentials; their contents cannot be safely
// classified by a key-name heuristic. Read paths hide the complete value.
var monitorPayloadKeys = []string{"body", "send_data", "send_message", "expect_data", "expect_reply", "expect_message", "expect_banner"}

// URLOrigin exposes no userinfo, path, query or fragment. Even a URL path may
// contain a bearer token. Invalid or dynamic URLs have no safe static origin.
func URLOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Opaque != "" || !slices.Contains([]string{"http", "https", "ws", "wss"}, u.Scheme) {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// MonitorTarget is the shared projection for API, lists, details and forms.
// Command targets are legacy free text; the executable has its own setting.
func MonitorTarget(kind, target string) string {
	switch kind {
	case "http", "http_multi", "websocket":
		return URLOrigin(target)
	case "command":
		return ""
	case "manual", "heartbeat":
		if target == kind {
			return target
		}
	case "docker":
		if safeHostIdentifier(target) && !strings.Contains(target, "..") {
			return target
		}
	case "tcp", "udp", "dns", "domain", "icmp", "tls", "grpc", "mqtt", "smtp", "ssh", "redis", "postgresql":
		if net.ParseIP(target) != nil || safeHostIdentifier(target) {
			return target
		}
		host, port, err := net.SplitHostPort(target)
		if err == nil && (net.ParseIP(host) != nil || safeHostIdentifier(host)) {
			number, err := strconv.Atoi(port)
			if err == nil && number > 0 && number <= 65535 {
				return target
			}
		}
	}
	return ""
}

func safeHostIdentifier(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, char := range host {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.') {
			return false
		}
	}
	return true
}

func mergeURL(submitted, stored, projection string, clear bool) (string, bool) {
	if !clear && stored != "" && (submitted == "" || submitted == projection) {
		return stored, stored != projection
	}
	return submitted, false
}

// MergeMonitorEdit prepares a complete credential-safe edit without changing
// either argument on failure. Blank/omitted private fields retain the current
// transaction's values; explicit clear paths remove them. An unchanged URL
// origin retains the whole saved URL unless that URL's path is explicitly
// cleared. Retaining a hidden credential never authorizes a new recipient.
func MergeMonitorEdit(next, old *storage.Monitor, secretKeys []string, clear map[string]bool, canRetarget bool) error {
	if next.Type != old.Type {
		return nil
	}
	normalizedClear := make(map[string]bool, len(clear))
	for path, remove := range clear {
		normalizedClear[strings.ToLower(path)] = remove
	}
	clear = normalizedClear
	prev, err := settingsObject(old.Settings, true)
	if err != nil {
		return err
	}
	omitted := len(next.Settings) == 0
	raw := next.Settings
	if omitted {
		raw = old.Settings
	}
	input, err := settingsObject(raw, omitted)
	if err != nil {
		return err
	}
	for _, object := range []map[string]any{prev, input} {
		if err := checkMonitorPrivateContainers(object); err != nil {
			return err
		}
	}
	prepared := *next
	var retainedTarget bool
	prepared.Target, retainedTarget = mergeURL(next.Target, old.Target, MonitorTarget(old.Type, old.Target), clear["target"])
	proxyChanged := !sameID(prepared.ProxyID, old.ProxyID)
	if retainedTarget && proxyChanged && !canRetarget {
		return credentialDestinationError()
	}
	oldOAuth, err := settingString(prev, "oauth2_token_url")
	if err != nil {
		return err
	}
	newOAuth, err := settingString(input, "oauth2_token_url")
	if err != nil {
		return err
	}
	if omitted && clear["oauth2_token_url"] {
		newOAuth = ""
	}
	resolvedOAuth, retainedOAuth := mergeURL(newOAuth, oldOAuth, URLOrigin(oldOAuth), clear["oauth2_token_url"])
	if omitted && oldOAuth != "" && oldOAuth != URLOrigin(oldOAuth) && !clear["oauth2_token_url"] {
		retainedOAuth = true
	}
	if resolvedOAuth != "" || oldOAuth != "" {
		input["oauth2_token_url"] = resolvedOAuth
	}
	if retainedOAuth && (proxyChanged || prepared.Target != old.Target) && !canRetarget {
		return credentialDestinationError()
	}
	changed := prepared.Target != old.Target || proxyChanged || resolvedOAuth != oldOAuth
	merge := monitorPrivateMerge{clear: clear, canRetarget: canRetarget}
	for _, key := range append(slices.Clone(secretKeys), monitorPayloadKeys...) {
		if err := merge.stringField(input, prev, key, key, omitted, changed); err != nil {
			return err
		}
	}
	oldCommand, err := settingString(prev, "command")
	if err != nil {
		return err
	}
	newCommand, err := settingString(input, "command")
	if err != nil {
		return err
	}
	if err := merge.args(input, prev, omitted, changed || newCommand != oldCommand); err != nil {
		return err
	}
	if err := merge.headers(input, prev, "", omitted, changed); err != nil {
		return err
	}
	if err := merge.steps(input, prev, omitted, changed); err != nil {
		return err
	}
	prepared.Settings, err = json.Marshal(input)
	if err != nil {
		return err
	}
	*next = prepared
	return nil
}

func credentialDestinationError() error {
	return fmt.Errorf("changing the credential destination requires replacing or clearing saved credentials")
}

// Validate private containers before redaction or identity matching. Invalid
// legacy containers must never be serialized unchanged, and the public five
// step limit also bounds the matching work before ordinary input validation.
func checkMonitorPrivateContainers(object map[string]any) error {
	if object["headers"] != nil {
		if _, ok := object["headers"].(map[string]any); !ok {
			return errAmbiguousSettings
		}
	}
	if object["steps"] == nil {
		return nil
	}
	steps, ok := object["steps"].([]any)
	if !ok {
		return errAmbiguousSettings
	}
	if len(steps) > 5 {
		return fmt.Errorf("multi-step settings must contain at most 5 steps")
	}
	for _, entry := range steps {
		step, ok := entry.(map[string]any)
		if !ok {
			return errAmbiguousSettings
		}
		if step["headers"] != nil {
			if _, ok := step["headers"].(map[string]any); !ok {
				return errAmbiguousSettings
			}
		}
	}
	return nil
}

func settingString(object map[string]any, key string) (string, error) {
	value := object[key]
	if value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", errAmbiguousSettings
	}
	return text, nil
}

type monitorPrivateMerge struct {
	clear       map[string]bool
	canRetarget bool
}

func (m monitorPrivateMerge) stringField(next, old map[string]any, key, path string, omitted, changed bool) error {
	previous, err := settingString(old, key)
	if err != nil {
		return err
	}
	submitted, err := settingString(next, key)
	if err != nil {
		return err
	}
	if m.clear[path] {
		next[key] = ""
	} else if previous != "" && (omitted || submitted == "") {
		if changed && !m.canRetarget {
			return credentialDestinationError()
		}
		next[key] = previous
	}
	return nil
}

func (m monitorPrivateMerge) args(next, old map[string]any, omitted, changed bool) error {
	arrays := make([][]any, 2)
	for i, object := range []map[string]any{old, next} {
		if object["args"] == nil {
			continue
		}
		value, ok := object["args"].([]any)
		if !ok {
			return errAmbiguousSettings
		}
		for _, arg := range value {
			if _, ok := arg.(string); !ok {
				return errAmbiguousSettings
			}
		}
		arrays[i] = value
	}
	if m.clear["args"] {
		next["args"] = []any{}
	} else if len(arrays[0]) > 0 && (omitted || len(arrays[1]) == 0) {
		if changed && !m.canRetarget {
			return credentialDestinationError()
		}
		next["args"] = arrays[0]
	}
	return nil
}

func (m monitorPrivateMerge) headers(next, old map[string]any, prefix string, omitted, changed bool) error {
	previous, _ := old["headers"].(map[string]any)
	submitted, _ := next["headers"].(map[string]any)
	if old["headers"] != nil && previous == nil || next["headers"] != nil && submitted == nil {
		return errAmbiguousSettings
	}
	oldByName := make(map[string]any, len(previous))
	for name, value := range previous {
		oldByName[strings.ToLower(name)] = value
	}
	for name := range submitted {
		path := strings.ToLower(prefix + "headers." + name)
		prior, err := settingString(oldByName, strings.ToLower(name))
		if err != nil {
			return err
		}
		value, err := settingString(submitted, name)
		if err != nil {
			return err
		}
		if m.clear[path] {
			submitted[name] = ""
		} else if prior != "" && (omitted || value == "") {
			if changed && !m.canRetarget {
				return credentialDestinationError()
			}
			submitted[name] = prior
		}
	}
	return nil
}

func (m monitorPrivateMerge) steps(next, old map[string]any, omitted, changed bool) error {
	previous, _ := old["steps"].([]any)
	submitted, _ := next["steps"].([]any)
	if old["steps"] != nil && previous == nil || next["steps"] != nil && submitted == nil {
		return errAmbiguousSettings
	}
	for i, entry := range submitted {
		step, ok := entry.(map[string]any)
		if !ok {
			return errAmbiguousSettings
		}
		name, err := settingString(step, "name")
		if err != nil {
			return err
		}
		var match map[string]any
		var matches []map[string]any
		for j, entry := range previous {
			candidate, ok := entry.(map[string]any)
			if !ok {
				return errAmbiguousSettings
			}
			oldName, err := settingString(candidate, "name")
			if err != nil {
				return err
			}
			oldURL, err := settingString(candidate, "url")
			if err != nil {
				return err
			}
			newURL, err := settingString(step, "url")
			if err != nil {
				return err
			}
			if (omitted && i == j) || (!omitted && oldName == name && (newURL == "" || newURL == oldURL || newURL == URLOrigin(oldURL))) {
				match = candidate
				matches = append(matches, candidate)
			}
		}
		prefix := "steps." + strconv.Itoa(i) + "."
		if len(matches) > 1 {
			for _, candidate := range matches {
				retained, err := m.retainsStepFields(step, candidate, prefix)
				if err != nil {
					return err
				}
				if retained {
					return fmt.Errorf("step %d has an ambiguous saved identity; provide replacement private fields or clear them explicitly", i+1)
				}
			}
			match = nil
		}
		oldURL, err := settingString(match, "url")
		if err != nil {
			return err
		}
		newURL, err := settingString(step, "url")
		if err != nil {
			return err
		}
		if omitted && m.clear[prefix+"url"] {
			newURL = ""
		}
		resolved, retained := mergeURL(newURL, oldURL, URLOrigin(oldURL), m.clear[prefix+"url"])
		if omitted && oldURL != "" && oldURL != URLOrigin(oldURL) && !m.clear[prefix+"url"] {
			retained = true
		}
		if retained && changed && !m.canRetarget {
			return credentialDestinationError()
		}
		step["url"] = resolved
		stepChanged := changed || resolved != oldURL
		if err := m.stringField(step, match, "body", prefix+"body", omitted, stepChanged); err != nil {
			return err
		}
		if err := m.headers(step, match, prefix, omitted, stepChanged); err != nil {
			return err
		}
	}
	return nil
}

func (m monitorPrivateMerge) retainsStepFields(next, old map[string]any, prefix string) (bool, error) {
	for _, key := range []string{"url", "body"} {
		previous, err := settingString(old, key)
		if err != nil {
			return false, err
		}
		submitted, err := settingString(next, key)
		if err != nil {
			return false, err
		}
		if m.clear[prefix+key] || previous == "" {
			continue
		}
		if key == "body" && submitted == "" || key == "url" && previous != submitted && (submitted == "" || submitted == URLOrigin(previous)) {
			return true, nil
		}
	}
	previous, _ := old["headers"].(map[string]any)
	submitted, _ := next["headers"].(map[string]any)
	oldByName := make(map[string]any, len(previous))
	for name, value := range previous {
		oldByName[strings.ToLower(name)] = value
	}
	for name := range submitted {
		if m.clear[strings.ToLower(prefix+"headers."+name)] {
			continue
		}
		value, err := settingString(submitted, name)
		if err != nil {
			return false, err
		}
		if value != "" {
			continue
		}
		stored, err := settingString(oldByName, strings.ToLower(name))
		if err != nil {
			return false, err
		}
		if stored != "" {
			return true, nil
		}
	}
	return false, nil
}

func sameID(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

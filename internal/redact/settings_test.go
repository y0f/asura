package redact

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/y0f/asura/v3/internal/storage"
)

func TestMonitorSettingsRedaction(t *testing.T) {
	for _, raw := range []string{
		`{"method":"GET","basic_auth_pass":"private-password","bearer_token":"private-bearer","oauth2_client_secret":"private-oauth","mtls_client_key":"private-key","password":"private-redis","headers":{"Authorization":"private-header","Arbitrary-Name":"private-custom"}}`,
		`{"steps":[{"name":"login","url":"https://example.com","headers":{"X-Session":"private-step"}}]}`,
		`{"password":{"value":"private-object"},"headers":{"X-Secret":["private-array"]}}`,
	} {
		monitor := &storage.Monitor{Settings: json.RawMessage(raw)}
		clean := Monitor(monitor)
		if strings.Contains(string(clean.Settings), "private-") {
			t.Fatalf("secret escaped redaction: %s", clean.Settings)
		}
		if string(monitor.Settings) != raw {
			t.Fatal("redaction modified the stored settings")
		}
		if !json.Valid(clean.Settings) {
			t.Fatalf("redaction returned invalid JSON: %s", clean.Settings)
		}
	}
	for _, raw := range []string{"", "null", "[]", `"private-invalid"`, `{"password":"private-invalid"`} {
		clean, _ := MonitorSettings(json.RawMessage(raw))
		if string(clean) != "{}" {
			t.Errorf("malformed settings %q returned %s, want empty object", raw, clean)
		}
	}
}

func TestNotificationRedaction(t *testing.T) {
	for kind, keys := range NotificationSecretKeys {
		t.Run(kind, func(t *testing.T) {
			fields := map[string]any{"non_secret": "visible"}
			for _, key := range keys {
				fields[key] = "private-" + key
			}
			raw, _ := json.Marshal(fields)
			channel := &storage.NotificationChannel{Type: kind, Settings: raw}
			clean := Notification(channel)
			if strings.Contains(string(clean.Settings), "private-") || !strings.Contains(string(clean.Settings), "visible") {
				t.Fatalf("unexpected redaction: %s", clean.Settings)
			}
			if string(channel.Settings) != string(raw) {
				t.Fatal("redaction modified stored channel")
			}
		})
	}
}

func TestMonitorHeaderRoundTrip(t *testing.T) {
	old := json.RawMessage(`{"headers":{"X-Token":"stored","Delete-Me":"removed"},"method":"GET"}`)
	for _, tc := range []struct {
		name, next, want string
		clear            map[string]bool
	}{
		{"keep case insensitive", `{"headers":{"x-token":""}}`, `{"headers":{"x-token":"stored"}}`, nil},
		{"replace", `{"headers":{"X-Token":"new"}}`, `{"headers":{"X-Token":"new"}}`, nil},
		{"remove row", `{"headers":{}}`, `{"headers":{}}`, nil},
		{"clear value", `{"headers":{"X-Token":""}}`, `{"headers":{"X-Token":""}}`, map[string]bool{"headers.X-Token": true}},
		{"clear case insensitive", `{"headers":{"X-Token":""}}`, `{"headers":{"X-Token":""}}`, map[string]bool{"headers.x-token": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mergeMonitorSettingsForTest(json.RawMessage(tc.next), old, tc.clear)
			if err != nil {
				t.Fatal(err)
			}
			var gotJSON, wantJSON any
			_ = json.Unmarshal(got, &gotJSON)
			_ = json.Unmarshal([]byte(tc.want), &wantJSON)
			g, _ := json.Marshal(gotJSON)
			w, _ := json.Marshal(wantJSON)
			if string(g) != string(w) {
				t.Fatalf("merge = %s, want %s", g, w)
			}
		})
	}
}

func TestMultistepSecretReorder(t *testing.T) {
	old := json.RawMessage(`{"steps":[{"name":"first","url":"https://first.example","headers":{"X-Token":"first-secret"}},{"name":"second","url":"https://second.example","headers":{"X-Token":"second-secret"}}]}`)
	next := json.RawMessage(`{"steps":[{"name":"second","url":"https://second.example","headers":{"X-Token":""}},{"name":"first","url":"https://first.example","headers":{"X-Token":""}}]}`)
	merged, err := mergeMonitorSettingsForTest(next, old, nil)
	if err != nil {
		t.Fatal(err)
	}
	var settings storage.MultiStepHTTPSettings
	if err := json.Unmarshal(merged, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Steps[0].Headers["X-Token"] != "second-secret" || settings.Steps[1].Headers["X-Token"] != "first-secret" {
		t.Fatalf("reordering attached the wrong credentials: %s", merged)
	}
	next = json.RawMessage(`{"steps":[{"name":"first","url":"https://changed.example","headers":{"X-Token":""}}]}`)
	merged, err = mergeMonitorSettingsForTest(next, old, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(merged), "secret") {
		t.Fatalf("copied stored credential to a changed endpoint: %s", merged)
	}
}

func TestMultistepAmbiguousIdentityRequiresReplacement(t *testing.T) {
	old := json.RawMessage(`{"steps":[{"name":"same","url":"https://example.com","headers":{"X-Token":"first-secret"}},{"name":"same","url":"https://example.com","headers":{"X-Token":"second-secret"}}]}`)
	next := json.RawMessage(`{"steps":[{"name":"same","url":"https://example.com","headers":{"X-Token":""}}]}`)
	if _, err := mergeMonitorSettingsForTest(next, old, nil); err == nil {
		t.Fatal("ambiguous saved step reused a credential")
	}
	cleared, err := mergeMonitorSettingsForTest(next, old, map[string]bool{"steps.0.headers.x-token": true})
	if err != nil || strings.Contains(string(cleared), "secret") {
		t.Fatalf("explicit clear = %s, %v", cleared, err)
	}
	next = json.RawMessage(`{"steps":[{"name":"same","url":"https://example.com","headers":{"X-Token":"new-value"}}]}`)
	replaced, err := mergeMonitorSettingsForTest(next, old, nil)
	if err != nil || !strings.Contains(string(replaced), "new-value") {
		t.Fatalf("replacement = %s, %v", replaced, err)
	}
}

// Exercise the same complete current-row merge used by API and form edits.
func mergeMonitorSettingsForTest(updated, old json.RawMessage, clear map[string]bool) (json.RawMessage, error) {
	next := &storage.Monitor{Type: "http", Target: "https://example.test", Settings: updated}
	previous := &storage.Monitor{Type: "http", Target: next.Target, Settings: old}
	if err := MergeMonitorEdit(next, previous, MonitorSecretKeys["http"], clear, false); err != nil {
		return nil, err
	}
	return next.Settings, nil
}

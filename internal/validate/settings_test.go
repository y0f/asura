package validate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/storage"
)

func TestMonitorSettingsRejectAmbiguousKeysAndTypes(t *testing.T) {
	for _, raw := range []string{
		`{"BEARER_TOKEN":"private"}`, `{"paſſword":"private"}`,
		`{"bearer_token":"private","bearer_token":null}`,
		`{"method":"GET","METHOD":"POST"}`, `{"timeout":10}`,
		`{"headers":{"Authorization":"first","Authorization":"second"}}`,
		`{"follow_redirects":"yes"}`, `null`, `[]`,
	} {
		t.Run(raw, func(t *testing.T) {
			m := validMonitor()
			m.Settings = json.RawMessage(raw)
			if err := ValidateMonitor(m); err == nil {
				t.Fatal("ambiguous or invalid settings accepted")
			} else if strings.Contains(err.Error(), "private") {
				t.Fatal("error echoed a credential")
			}
		})
	}
	m := validMonitor()
	m.Settings = []byte(`{"headers":{"X-Custom-Token":"private"},"body":"{\"CaseSensitive\":1}"}`)
	if err := ValidateMonitor(m); err != nil {
		t.Fatalf("custom header/body spelling rejected: %v", err)
	}
	m.Type = "http_multi"
	m.Settings = []byte(`{"steps":[{"url":"https://example.test","Method":"GET"}]}`)
	if err := ValidateMonitor(m); err == nil {
		t.Fatal("nested noncanonical typed key accepted")
	}
}

func TestPassiveMonitorLimitsAndAssertionsAreValidated(t *testing.T) {
	for _, kind := range []string{"manual", "heartbeat"} {
		for _, field := range []string{"interval", "timeout", "threshold", "settings", "assertions"} {
			t.Run(kind+"/"+field, func(t *testing.T) {
				m := validMonitor()
				m.Type, m.Target = kind, ""
				switch field {
				case "interval":
					m.Interval = 0
				case "timeout":
					m.Timeout = 301
				case "threshold":
					m.FailureThreshold = 0
				case "settings":
					m.Settings = []byte(`{"unknown":true}`)
				case "assertions":
					m.Assertions = []byte(`{"groups":[{"conditions":[{"type":"unrecognized"}]}]}`)
				}
				if err := ValidateMonitor(m); err == nil {
					t.Fatal("passive monitor bypassed validation")
				}
			})
		}
	}
	for _, grace := range []string{"-1", "86401", "1.5"} {
		m := validMonitor()
		m.Type = "heartbeat"
		m.Settings = []byte(`{"grace":` + grace + `}`)
		if err := ValidateMonitor(m); err == nil {
			t.Fatalf("invalid heartbeat grace accepted: %s", grace)
		}
	}
}

func TestStepURLsValidateLiteralSchemesBeforeInterpolation(t *testing.T) {
	variables := map[string]bool{"token": true, "base": true}
	for _, raw := range []string{"https://example.test/{{token}}", "https://{{token}}/path", "{{base}}", "{{base}}/path?token={{token}}"} {
		if !validStepURL(raw, variables) {
			t.Errorf("valid URL template rejected: %s", raw)
		}
	}
	for _, raw := range []string{"ftp://{{token}}", "javascript:{{token}}", "//{{token}}/path", "ht{{token}}://example.test", "https://example.test/{{missing}}", "https://example.test/{{token", "https://example.test/\n{{token}}"} {
		if validStepURL(raw, variables) {
			t.Errorf("invalid URL template accepted: %s", raw)
		}
	}
}

func TestNotificationSettingsUseProviderSchema(t *testing.T) {
	fixtures := map[string]string{
		"webhook":    `{"url":"https://example.test/hook"}`,
		"email":      `{"host":"mail.example.test","from":"from@example.test","to":["to@example.test"]}`,
		"telegram":   `{"bot_token":"private","chat_id":"123"}`,
		"discord":    `{"webhook_url":"https://example.test/hook"}`,
		"slack":      `{"webhook_url":"https://example.test/hook"}`,
		"ntfy":       `{"topic":"operations"}`,
		"teams":      `{"webhook_url":"https://example.test/hook"}`,
		"pagerduty":  `{"routing_key":"private"}`,
		"opsgenie":   `{"api_key":"private","region":"eu"}`,
		"pushover":   `{"user_key":"private","app_token":"private"}`,
		"googlechat": `{"webhook_url":"https://example.test/hook"}`,
		"matrix":     `{"homeserver":"https://example.test","access_token":"private","room_id":"room"}`,
		"gotify":     `{"server_url":"https://example.test","app_token":"private"}`,
	}
	for kind, raw := range fixtures {
		t.Run(kind, func(t *testing.T) {
			channel := &storage.NotificationChannel{Name: "alerts", Type: kind, Settings: []byte(raw)}
			if err := ValidateNotificationChannel(channel); err != nil {
				t.Fatalf("valid provider settings rejected: %v", err)
			}
			for _, invalid := range []string{`{}`, `null`, `[]`, raw[:len(raw)-1] + `,"unknown":"private"}`} {
				channel.Settings = []byte(invalid)
				if err := ValidateNotificationChannel(channel); err == nil {
					t.Fatalf("invalid provider settings accepted: %s", invalid)
				} else if strings.Contains(err.Error(), "private") {
					t.Fatal("error echoed a credential")
				}
			}
		})
	}
}

func TestNumericHeaderAndJSONAssertionThresholdsAreFinite(t *testing.T) {
	for _, kind := range []string{"header", "json_path"} {
		for _, value := range []string{"", "unavailable", "NaN", "Inf", "-Inf", "1e999"} {
			raw, err := json.Marshal(map[string]any{"groups": []any{map[string]any{"conditions": []any{map[string]string{
				"type": kind, "operator": "lte", "target": "load", "value": value,
			}}}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := validateAssertions(raw); err == nil {
				t.Errorf("%s accepted nonnumeric threshold %q", kind, value)
			}
		}
	}
	for _, raw := range []string{
		`{"groups":[{"conditions":[{"type":"header","operator":"lte","target":"Load","value":"0.25"}]}]}`,
		`{"groups":[{"conditions":[{"type":"json_path","operator":"gt","target":"load","value":"1e2"}]}]}`,
		`{"groups":[{"conditions":[{"type":"header","operator":"eq","target":"Status","value":"unavailable"}]}]}`,
	} {
		if err := validateAssertions(json.RawMessage(raw)); err != nil {
			t.Errorf("valid assertion rejected: %v", err)
		}
	}
}

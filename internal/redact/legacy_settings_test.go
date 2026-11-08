package redact

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/storage"
)

func TestLegacySettingsCannotExposeCaseFoldedCredentials(t *testing.T) {
	for _, raw := range []string{
		`{"BEARER_TOKEN":"private-token","paſſword":"private-password"}`,
		`{"HEADERS":{"Authorization":"private-header"}}`,
		`{"STEPS":[{"URL":"https://example.test","Headers":{"Cookie":"private-cookie"}}]}`,
		`{"bearer_token":"private-first","BEARER_TOKEN":null}`,
		`{"headers":{"Authorization":"private-first"},"headers":{"Accept":"application/json"}}`,
	} {
		clean, _ := MonitorSettings(json.RawMessage(raw))
		if strings.Contains(string(clean), "private-") {
			t.Errorf("legacy credentials escaped redaction: %s", clean)
		}
	}
	for _, raw := range []string{`{"BOT_TOKEN":"private-bot"}`, `{"WEBHOOK_URL":"private-hook"}`, `{"paſſword":"private-password"}`} {
		clean := Notification(&storage.NotificationChannel{Settings: json.RawMessage(raw)})
		if strings.Contains(string(clean.Settings), "private-") {
			t.Errorf("legacy notification credentials escaped redaction: %s", clean.Settings)
		}
	}
}

func TestLegacyCredentialDestinationsFailClosed(t *testing.T) {
	for _, raw := range []string{
		`{"BEARER_TOKEN":"private-token"}`,
		`{"HEADERS":{"Authorization":"private-header"}}`,
		`{"bearer_token":"private-token","bearer_token":null}`,
		`{"bearer_token":"private-token","BEARER_TOKEN":null}`,
		`{"headers":{"Authorization":"private-token"},"headers":{}}`,
	} {
		old := &storage.Monitor{Type: "http", Target: "https://old.example", Settings: json.RawMessage(raw)}
		next := &storage.Monitor{Type: old.Type, Target: "https://new.example"}
		if err := MergeMonitorEdit(next, old, MonitorSecretKeys["http"], nil, false); err == nil {
			t.Errorf("changed destination accepted hidden legacy credential: %s", raw)
		} else if strings.Contains(err.Error(), "private-") {
			t.Fatal("credential appeared in error")
		}
	}
}

func TestSettingsRedactionPreservesIntegerPrecision(t *testing.T) {
	raw := json.RawMessage(`{"expected_size":9007199254740993,"password":"private-password"}`)
	clean, _ := MonitorSettings(raw)
	if !strings.Contains(string(clean), "9007199254740993") {
		t.Fatalf("redaction rounded a configuration value: %s", clean)
	}
}

func TestSecretMergeRejectsAmbiguityBeforeNormalization(t *testing.T) {
	old := json.RawMessage(`{"bearer_token":"private-stored"}`)
	for _, raw := range []string{
		`{"bearer_token":"","bearer_token":null}`,
		`{"bearer_token":"","BEARER_TOKEN":null}`,
		`{"headers":{"Authorization":"first","Authorization":"second"}}`,
		`{"headers":{"Authorization":"first","authorization":"second"}}`,
		`{"steps":[{"headers":{"X-Key":"first"},"headers":{}}]}`,
		`{"bearer_token":17}`, `{"bearer_token":[]}`, `{"bearer_token":true}`,
		`{} {}`, `null`, `[]`,
	} {
		if merged, err := MergeSecrets(json.RawMessage(raw), old, MonitorSecretKeys["http"], nil); err == nil || merged != nil {
			t.Errorf("ambiguous submitted settings normalized into accepted edit: %s, %v", merged, err)
		}
	}
	for _, raw := range []string{`{"bearer_token":"private-first","bearer_token":null}`, `{"bearer_token":"private-first","BEARER_TOKEN":null}`} {
		if merged, err := MergeSecrets(json.RawMessage(`{}`), json.RawMessage(raw), MonitorSecretKeys["http"], nil); err == nil || merged != nil {
			t.Errorf("ambiguous saved credentials silently replaced: %s, %v", merged, err)
		}
	}
}

func TestLegacySecretsPreserveUnambiguousValues(t *testing.T) {
	old := json.RawMessage(`{"BEARER_TOKEN":"private-stored","HEADERS":{"X-Token":"private-header"}}`)
	merged, err := mergeMonitorSettingsForTest(json.RawMessage(`{"bearer_token":"","headers":{"x-token":""},"expected_size":9007199254740993}`), old, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"bearer_token":"private-stored"`, `"x-token":"private-header"`, "9007199254740993"} {
		if !strings.Contains(string(merged), expected) {
			t.Errorf("legacy value was lost during edit: %s", merged)
		}
	}
}

package redact

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNotificationCredentialDestinationAndCosmeticChanges(t *testing.T) {
	for _, tc := range []struct {
		kind, old, edit string
		blocked         bool
	}{
		{"email", `{"host":"smtp.test","password":"saved-secret"}`, `{"host":"other.test","password":""}`, true},
		{"email", `{"host":"smtp.test","password":"saved-secret","username":"first"}`, `{"host":"smtp.test","username":"second"}`, true},
		{"email", `{"host":"smtp.test","password":"saved-secret","tls_mode":"starttls_required"}`, `{"host":"smtp.test","tls_mode":"none"}`, true},
		{"email", `{"host":"smtp.test","password":"saved-secret","to":["one@test.example"]}`, `{"host":"smtp.test","to":["other@test.example"]}`, true},
		{"matrix", `{"homeserver":"https://matrix.test","access_token":"saved-secret","room_id":"old"}`, `{"homeserver":"https://other.test","room_id":"old"}`, true},
		{"matrix", `{"homeserver":"https://matrix.test","access_token":"saved-secret","room_id":"old"}`, `{"homeserver":"https://matrix.test","room_id":"new"}`, true},
		{"gotify", `{"server_url":"https://gotify.test","app_token":"saved-secret","priority":1}`, `{"server_url":"https://gotify.test","priority":2}`, false},
		{"gotify", `{"server_url":"https://gotify.test","app_token":"saved-secret"}`, `{"server_url":"https://other.test"}`, true},
		{"telegram", `{"bot_token":"saved-secret","chat_id":"one"}`, `{"chat_id":"two"}`, true},
		{"slack", `{"webhook_url":"https://secret.test","channel":"one"}`, `{"channel":"two"}`, true},
		{"opsgenie", `{"api_key":"saved-secret","region":"us"}`, `{"region":"eu"}`, true},
		{"pushover", `{"app_token":"saved-secret","user_key":"user-secret","device":"one"}`, `{"device":"two"}`, true},
		{"webhook", `{"url":"https://hook.test","secret":"saved-secret"}`, `{"url":"https://other.test"}`, true},
		{"webhook", `{"url":"https://hook.test","secret":"saved-secret"}`, `{"url":"","secret":""}`, false},
		{"email", `{"HOST":"smtp.test","PASSWORD":"saved-secret"}`, `{"host":"other.test"}`, true},
		{"email", `{"host":"smtp.test","password":"saved-secret"}`, `{"host":"other.test","password":"new-secret"}`, false},
	} {
		t.Run(tc.kind+tc.edit, func(t *testing.T) {
			merged, err := MergeNotificationSecrets(tc.kind, json.RawMessage(tc.edit), json.RawMessage(tc.old), nil, false)
			if (err != nil) != tc.blocked {
				t.Fatalf("destination check: %v", err)
			}
			if err != nil && (merged != nil || strings.Contains(err.Error(), "saved-secret")) {
				t.Fatal("rejected edit returned credentials")
			}
			if _, err := MergeNotificationSecrets(tc.kind, json.RawMessage(tc.edit), json.RawMessage(tc.old), nil, true); err != nil {
				t.Fatalf("super administrator retarget rejected: %v", err)
			}
		})
	}
	got, err := MergeNotificationSecrets("email", json.RawMessage(`{"host":"other.test"}`), json.RawMessage(`{"host":"smtp.test","password":"saved-secret"}`), map[string]bool{"password": true}, false)
	if err != nil || strings.Contains(string(got), "saved-secret") {
		t.Fatal("explicit clearing did not allow a new destination")
	}
}

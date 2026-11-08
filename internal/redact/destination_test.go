package redact

import (
	"encoding/json"
	"testing"

	"github.com/y0f/asura/v2/internal/storage"
)

func TestMonitorCredentialDestination(t *testing.T) {
	old := &storage.Monitor{Type: "http", Target: "https://old.example", Settings: json.RawMessage(`{"bearer_token":"stored-token","headers":{"X-Token":"stored-header"}}`)}
	for _, tc := range []struct {
		name, target, settings string
		proxy                  bool
		clear                  map[string]bool
		blocked                bool
	}{
		{"same recipient", old.Target, `{"bearer_token":"","headers":{"X-Token":""}}`, false, nil, false},
		{"new target", "https://new.example", `{"bearer_token":"","headers":{"X-Token":""}}`, false, nil, true},
		{"omitted settings", "https://new.example", "", false, nil, true},
		{"new proxy", old.Target, `{"bearer_token":"","headers":{"X-Token":""}}`, true, nil, true},
		{"explicit replacement", "https://new.example", `{"bearer_token":"new-token","headers":{"X-Token":"new-header"}}`, false, nil, false},
		{"explicit clear", "https://new.example", `{"bearer_token":"","headers":{"X-Token":""}}`, false, map[string]bool{"bearer_token": true, "headers.x-token": true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := &storage.Monitor{Type: old.Type, Target: tc.target, Settings: json.RawMessage(tc.settings)}
			if tc.proxy {
				id := int64(2)
				next.ProxyID = &id
			}
			if err := MergeMonitorEdit(next, old, MonitorSecretKeys[old.Type], tc.clear, false); (err != nil) != tc.blocked {
				t.Fatalf("destination check = %v, blocked=%t", err, tc.blocked)
			}
		})
	}
	old.Settings = json.RawMessage(`{"oauth2_token_url":"https://old.example/token","oauth2_client_secret":"stored-secret"}`)
	next := *old
	next.Settings = json.RawMessage(`{"oauth2_token_url":"https://new.example/token","oauth2_client_secret":""}`)
	if err := MergeMonitorEdit(&next, old, MonitorSecretKeys[old.Type], nil, false); err == nil {
		t.Fatal("OAuth credential was retained for changed issuer")
	}
	old.Type = "http_multi"
	old.Settings = json.RawMessage(`{"steps":[{"name":"step","url":"https://same.example","headers":{"X-Token":"stored-secret"}}]}`)
	next = *old
	id := int64(2)
	next.ProxyID = &id
	next.Settings = json.RawMessage(`{"steps":[{"name":"step","url":"https://same.example","headers":{"X-Token":""}}]}`)
	if err := MergeMonitorEdit(&next, old, nil, nil, false); err == nil {
		t.Fatal("multi-step credential was retained through a new proxy")
	}
	if err := MergeMonitorEdit(&next, old, nil, map[string]bool{"steps.0.headers.x-token": true}, false); err != nil {
		t.Fatalf("explicit multi-step clear blocked: %v", err)
	}
}

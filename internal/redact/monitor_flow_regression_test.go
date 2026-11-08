package redact

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/storage"
)

// A saved authenticated source can produce credentials at runtime. Checking
// each step's stored fields independently misses a new sink for its output.
func TestMonitorPrivateSourceCannotAuthorizeChangedFlow(t *testing.T) {
	old := &storage.Monitor{Type: "http_multi", Target: "https://source.test", Settings: json.RawMessage(`{
		"steps":[
			{"name":"login","url":"https://source.test/login","method":"POST","body":"private-login-credential","extract_var":"token","extract_json":"access_token"},
			{"name":"public-health","url":"https://sink.test/health","method":"GET"}
		]}`)}
	for _, tc := range []struct {
		name string
		edit func(*storage.MultiStepHTTPSettings)
	}{
		{"append recipient", func(s *storage.MultiStepHTTPSettings) {
			s.Steps = append(s.Steps, storage.HTTPStep{Name: "new sink", URL: "https://new-sink.test/receive", Method: "POST", Body: "{{token}}"})
		}},
		{"replace recipient", func(s *storage.MultiStepHTTPSettings) {
			s.Steps[1].URL, s.Steps[1].Method, s.Steps[1].Body = "https://new-sink.test/receive", "POST", "{{token}}"
		}},
		{"existing recipient body", func(s *storage.MultiStepHTTPSettings) {
			s.Steps[1].Method, s.Steps[1].Body = "POST", "{{token}}"
		}},
		{"existing recipient header", func(s *storage.MultiStepHTTPSettings) {
			s.Steps[1].Headers = map[string]string{"X-Extracted-Credential": "{{token}}"}
		}},
		{"change extractor", func(s *storage.MultiStepHTTPSettings) {
			s.Steps[0].ExtractJSON = "refresh_token"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := Monitor(old)
			var submitted storage.MultiStepHTTPSettings
			if err := json.Unmarshal(next.Settings, &submitted); err != nil {
				t.Fatal(err)
			}
			tc.edit(&submitted)
			var err error
			next.Settings, err = json.Marshal(submitted)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(next)
			if err := MergeMonitorEdit(next, old, nil, nil, false); err == nil {
				t.Fatal("ordinary flow edit reused the saved private login credential")
			} else if strings.Contains(err.Error(), "private-login-credential") {
				t.Fatal("denial disclosed the saved credential")
			}
			after, _ := json.Marshal(next)
			if string(before) != string(after) {
				t.Fatal("rejected flow edit mutated caller input")
			}
		})
	}
}

func TestMonitorPrivateFlowPreservesSafeEditsAndExplicitReplacement(t *testing.T) {
	old := &storage.Monitor{Type: "http_multi", Target: "https://source.test", Settings: json.RawMessage(`{"steps":[{"name":"login","url":"https://source.test/login","method":"POST","body":"private-login","extract_var":"token","extract_json":"access_token"},{"name":"sink","url":"https://sink.test/health","method":"GET"}]}`)}
	next := Monitor(old)
	next.Name = "metadata only"
	if err := MergeMonitorEdit(next, old, nil, nil, false); err != nil || !sameSettings(next.Settings, old.Settings) {
		t.Fatalf("unchanged masked chain failed: %v", err)
	}
	for _, replace := range []string{"clear", "replace", "admin"} {
		next := &storage.Monitor{Type: old.Type, Target: old.Target, Settings: json.RawMessage(`{"steps":[{"name":"login","url":"https://source.test/login","method":"POST","body":"","extract_var":"token","extract_json":"access_token"},{"name":"new sink","url":"https://new.test/receive","method":"POST","body":"{{token}}"}]}`)}
		clear := map[string]bool{}
		if replace == "clear" {
			clear["steps.0.body"] = true
		}
		if replace == "replace" {
			next.Settings = json.RawMessage(strings.Replace(string(next.Settings), `"body":""`, `"body":"known-replacement"`, 1))
		}
		if err := MergeMonitorEdit(next, old, nil, clear, replace == "admin"); err != nil {
			t.Fatalf("explicit %s rejected: %v", replace, err)
		}
	}
	next = Monitor(old)
	var object map[string]any
	if err := json.Unmarshal(next.Settings, &object); err != nil {
		t.Fatal(err)
	}
	steps := object["steps"].([]any)
	steps[0], steps[1] = steps[1], steps[0]
	next.Settings, _ = json.Marshal(object)
	if err := MergeMonitorEdit(next, old, nil, nil, false); err == nil {
		t.Fatal("reordering an extracted-variable chain reused saved private fields")
	}
}

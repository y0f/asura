package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestAgentTypedSecretMatchingUsesCheckerCaseRules(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	agent := &Agent{Name: "explicit delegate", Enabled: true}
	if err := s.CreateAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	mon := newTestMonitor(t, s, "legacy settings")
	mon.AgentEnabled = true
	if err := s.UpdateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMonitorAgentIDs(ctx, mon.ID, []int64{agent.ID}); err != nil {
		t.Fatal(err)
	}
	var settings []string
	for _, key := range agentSecretKeys {
		settings = append(settings,
			fmt.Sprintf(`{%q:"private"}`, strings.ToUpper(key)),
			fmt.Sprintf(`{%q:"private"}`, strings.ToUpper(key[:1])+key[1:]),
			fmt.Sprintf(`{%q:"",%q:"private"}`, key, strings.ToUpper(key)),
			fmt.Sprintf(`{%q:"private",%q:null}`, key, key),
		)
	}
	settings = append(settings, `{"paſſword":"private"}`, `{"BEARER_TOKEN":"private","bearer_token":null}`, `{"password":{}}`, `null`, `[]`, `1`, `"secret"`, `{"password":`, `{} {}`)
	for _, raw := range settings {
		t.Run(raw, func(t *testing.T) {
			// Model pre-validation legacy data, including malformed objects.
			if _, err := s.writeDB.ExecContext(ctx, `UPDATE monitors SET settings=? WHERE id=?`, raw, mon.ID); err != nil {
				// SQLite JSON constraints may already reject malformed documents.
				if json.Valid([]byte(raw)) {
					t.Fatal(err)
				}
				if agentMayReceiveSettings(raw, true) || sanitizeSettingsForAgent([]byte(raw)) != nil {
					t.Fatal("malformed settings were allowed or returned unchanged")
				}
				return
			}
			jobs, err := s.ListAgentJobs(ctx, agent.ID)
			if err != nil || len(jobs) != 0 {
				t.Fatalf("job exposed typed or ambiguous settings: %+v, %v", jobs, err)
			}
			allowed, err := s.AgentMayCheckMonitor(ctx, agent.ID, mon.ID)
			if err != nil || allowed {
				t.Fatalf("direct delegation check accepted settings: %v, %v", allowed, err)
			}
			current, err := s.GetMonitor(ctx, mon.ID)
			if err != nil {
				t.Fatal(err)
			}
			result := &CheckResult{MonitorID: mon.ID, AgentID: &agent.ID, Status: "down"}
			if err := s.RecordMonitorCheck(ctx, current, result, nil); !errors.Is(err, ErrStaleMonitorCheck) || result.ID != 0 {
				t.Fatalf("result bypassed current typed-secret guard: id=%d, %v", result.ID, err)
			}
		})
	}
	checks, err := s.ListCheckResults(ctx, mon.ID, Pagination{Page: 1, PerPage: 10})
	if err != nil || checks.Total != 0 {
		t.Fatalf("rejected settings left history: %+v, %v", checks, err)
	}
}

func TestAgentSecretSanitizerRemovesEveryCaseVariant(t *testing.T) {
	for _, key := range agentSecretKeys {
		for _, variant := range []string{key, strings.ToUpper(key), strings.ToUpper(key[:1]) + key[1:]} {
			raw := []byte(fmt.Sprintf(`{%q:"private","precise":9007199254740993,"headers":{"Authorization":"explicit"}}`, variant))
			clean := sanitizeSettingsForAgent(raw)
			if strings.Contains(string(clean), "private") || !strings.Contains(string(clean), "9007199254740993") || !strings.Contains(string(clean), "explicit") {
				t.Fatalf("sanitizer leaked typed credential or changed permitted data: %s", clean)
			}
		}
	}
	for _, raw := range []string{`{"bearer_token":"private","BEARER_TOKEN":null}`, `{"password":"private","password":null}`, `null`, `[]`, `{`} {
		if sanitizeSettingsForAgent([]byte(raw)) != nil || !settingsHaveAgentSecrets([]byte(raw)) {
			t.Fatalf("ambiguous/malformed settings did not fail closed: %s", raw)
		}
	}
	for _, raw := range []string{`{"BEARER_TOKEN":"","headers":{"Authorization":"explicit"}}`, `{"PASSWORD":null}`, `{}`} {
		if !agentMayReceiveSettings(raw, true) {
			t.Fatalf("empty credential blocked explicit ordinary job: %s", raw)
		}
	}
}

package httputil

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/storage"
)

func TestDiagnosticProjectionsRequireSeparateGrantAndKeepStoredData(t *testing.T) {
	check := &storage.CheckResult{ID: 3, MonitorID: 1, Status: "down", ResponseTime: 42, StatusCode: 503,
		Message: "private-error", Headers: "private-cookie", Body: "private-body", BodyHash: "private-hash", DNSRecords: "private-record", CertFingerprint: "private-fingerprint", AgentName: "private-agent"}
	change := &storage.ContentChange{ID: 4, MonitorID: 1, OldHash: "private-old", NewHash: "private-new", Diff: "private-diff", OldBody: "private-old-body", NewBody: "private-new-body"}
	inc := &storage.Incident{ID: 5, MonitorID: 1, Cause: "private-cause"}
	events := []*storage.IncidentEvent{{ID: 6, IncidentID: 5, Type: "check_failed", Message: "private-event"}}
	mon := &storage.Monitor{ID: 1, Type: "http", Assertions: json.RawMessage(`{"groups":[{"conditions":[{"type":"body_contains","value":"private-assertion"}]}]}`)}
	for _, diagnostic := range []bool{false, true} {
		permissions := []string{"monitors.read", "incidents.read"}
		if diagnostic {
			permissions = append(permissions, "diagnostics.read")
		}
		ctx := context.WithValue(context.Background(), CtxKeyAPIKey, &config.APIKeyConfig{Permissions: permissions})
		access, allowed := DiagnosticReadAccess(ctx, "monitors.read", 1, nil)
		if allowed != diagnostic || len(access) != 1+btoi(diagnostic) {
			t.Fatal("actual diagnostic read missing action intersection")
		}
		checks, err := CheckResultsResponse(ctx, &storage.PaginatedResult{Data: []*storage.CheckResult{check}}, allowed)
		if err != nil {
			t.Fatal(err)
		}
		changes, err := ContentChangesResponse(&storage.PaginatedResult{Data: []*storage.ContentChange{change}}, allowed)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal([]any{checks, changes, IncidentReadResponse(ctx, inc), IncidentEventsResponse(events, allowed), MonitorReadResponse(ctx, mon)})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(payload), "private-") != diagnostic || strings.Contains(string(payload), "private-agent") {
			t.Fatalf("diagnostic/agent-name boundary failed: %s", payload)
		}
		if got := checks.Data.([]*storage.CheckResult)[0]; got.StatusCode != 503 || got.ResponseTime != 42 {
			t.Fatal("ordinary measured data lost")
		}
	}
	if check.Body != "private-body" || change.Diff != "private-diff" || inc.Cause != "private-cause" || events[0].Message != "private-event" || len(mon.Assertions) == 0 {
		t.Fatal("projection changed stored diagnostics")
	}
	ctx := context.WithValue(context.Background(), CtxKeyAPIKey, &config.APIKeyConfig{Permissions: []string{"diagnostics.read"}})
	if _, allowed := DiagnosticReadAccess(ctx, "monitors.read", 1, nil); allowed || len(MonitorReadResponse(ctx, mon).Assertions) != 0 {
		t.Fatal("diagnostics alone granted monitor access")
	}
	if _, err := CheckResultsResponse(ctx, &storage.PaginatedResult{Data: map[string]string{"private": "wrong shape"}}, true); err == nil {
		t.Fatal("unexpected result bypassed projection")
	}
}

func btoi(v bool) int {
	if v {
		return 1
	}
	return 0
}

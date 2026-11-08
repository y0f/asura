package redact

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/storage"
)

func TestDiagnosticExecutionExpressionsReadAndEditPreservation(t *testing.T) {
	old := &storage.Monitor{Type: "http_multi", Target: "https://example.test", Settings: json.RawMessage(`{"steps":[{"name":"lookup","url":"https://example.test","extract_var":"session","extract_regex":"private-regex(.*)","extract_json":"private.selector"}]}`),
		Assertions: json.RawMessage(`{"groups":[{"conditions":[{"type":"body_contains","value":"private-assertion"}]}]}`)}
	for _, diagnostics := range []bool{false, true} {
		next := MonitorWithDiagnostics(old, diagnostics)
		encoded, _ := json.Marshal(next)
		if strings.Contains(string(encoded), "private") != diagnostics || !strings.Contains(string(encoded), "session") {
			t.Fatal("execution expressions or public variable metadata crossed read boundary")
		}
		if err := MergeMonitorEdit(next, old, nil, nil, false); err != nil || !sameSettings(next.Settings, old.Settings) || string(next.Assertions) != string(old.Assertions) {
			t.Fatalf("masked expressions did not survive unrelated edit: %v", err)
		}
	}
	for _, empty := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`{}`), json.RawMessage(`{"groups":[]}`)} {
		next := Monitor(old)
		next.Assertions = empty
		if err := MergeMonitorEdit(next, old, nil, nil, false); err != nil || string(next.Assertions) != string(old.Assertions) {
			t.Fatalf("blank assertion editor erased stored definitions: %v", err)
		}
	}
	next := Monitor(old)
	clear := map[string]bool{"assertions": true, "steps.0.extract_regex": true, "steps.0.extract_json": true}
	if err := MergeMonitorEdit(next, old, nil, clear, false); err != nil || string(next.Assertions) != `[]` || strings.Contains(string(next.Settings), "private") {
		t.Fatalf("explicit expression clearing failed: %v", err)
	}
	if !strings.Contains(string(old.Settings), "private") || len(old.Assertions) == 0 {
		t.Fatal("projection or merge mutated stored expressions")
	}
}

package assertion

import (
	"encoding/json"
	"testing"
)

func cs(operator string, groups ...ConditionGroup) json.RawMessage {
	raw, _ := json.Marshal(ConditionSet{Operator: operator, Groups: groups})
	return raw
}

func group(operator string, assertions ...Assertion) ConditionGroup {
	return ConditionGroup{Operator: operator, Conditions: assertions}
}

func TestStatusCodeAssertion(t *testing.T) {
	raw := cs("and", group("and", Assertion{Type: "status_code", Operator: "eq", Value: "200"}))

	result := Evaluate(raw, 200, "", nil, 100, nil, nil)
	if !result.Pass {
		t.Fatal("expected pass")
	}

	result = Evaluate(raw, 500, "", nil, 100, nil, nil)
	if result.Pass {
		t.Fatal("expected fail")
	}
}

func TestBodyContainsAssertion(t *testing.T) {
	raw := cs("and", group("and", Assertion{Type: "body_contains", Operator: "contains", Value: "hello"}))

	result := Evaluate(raw, 200, "hello world", nil, 100, nil, nil)
	if !result.Pass {
		t.Fatal("expected pass")
	}

	result = Evaluate(raw, 200, "goodbye", nil, 100, nil, nil)
	if result.Pass {
		t.Fatal("expected fail")
	}
}

func TestBodyRegexAssertion(t *testing.T) {
	raw := cs("and", group("and", Assertion{Type: "body_regex", Operator: "matches", Value: `\d{3}`}))

	result := Evaluate(raw, 200, "code 200 ok", nil, 100, nil, nil)
	if !result.Pass {
		t.Fatal("expected pass")
	}

	result = Evaluate(raw, 200, "no numbers", nil, 100, nil, nil)
	if result.Pass {
		t.Fatal("expected fail")
	}
}

func TestJSONPathAssertion(t *testing.T) {
	body := `{"status":"ok","data":{"count":42},"items":[{"id":1},{"id":2}]}`

	tests := []struct {
		target   string
		operator string
		value    string
		pass     bool
	}{
		{"status", "eq", "ok", true},
		{"data.count", "eq", "42", true},
		{"items[0].id", "eq", "1", true},
		{"items[1].id", "eq", "2", true},
		{"missing", "exists", "", false},
		{"status", "exists", "", true},
	}

	for _, tt := range tests {
		raw := cs("and", group("and", Assertion{Type: "json_path", Target: tt.target, Operator: tt.operator, Value: tt.value}))
		result := Evaluate(raw, 200, body, nil, 100, nil, nil)
		if result.Pass != tt.pass {
			t.Fatalf("json_path %s %s %s: expected pass=%v, got %v (msg: %s)",
				tt.target, tt.operator, tt.value, tt.pass, result.Pass, result.Message)
		}
	}
}

func TestHeaderAssertion(t *testing.T) {
	headers := map[string]string{"Content-Type": "application/json"}
	raw := cs("and", group("and", Assertion{Type: "header", Target: "Content-Type", Operator: "contains", Value: "json"}))

	result := Evaluate(raw, 200, "", headers, 100, nil, nil)
	if !result.Pass {
		t.Fatal("expected pass")
	}
}

func TestResponseTimeAssertion(t *testing.T) {
	raw := cs("and", group("and", Assertion{Type: "response_time", Operator: "lt", Value: "500"}))

	result := Evaluate(raw, 200, "", nil, 200, nil, nil)
	if !result.Pass {
		t.Fatal("expected pass: 200 < 500")
	}

	result = Evaluate(raw, 200, "", nil, 600, nil, nil)
	if result.Pass {
		t.Fatal("expected fail: 600 < 500 should fail")
	}
}

func TestDegradedAssertion(t *testing.T) {
	raw := cs("and", group("and", Assertion{Type: "response_time", Operator: "lt", Value: "100", Degraded: true}))

	result := Evaluate(raw, 200, "", nil, 200, nil, nil)
	if result.Pass {
		t.Fatal("expected fail")
	}
	if !result.Degraded {
		t.Fatal("expected degraded flag")
	}
}

func TestDNSRecordAssertion(t *testing.T) {
	records := []string{"1.2.3.4", "5.6.7.8"}
	raw := cs("and", group("and", Assertion{Type: "dns_record", Operator: "contains", Value: "1.2.3.4"}))

	result := Evaluate(raw, 0, "", nil, 0, nil, records)
	if !result.Pass {
		t.Fatal("expected pass")
	}
}

func TestWalkJSONPath(t *testing.T) {
	jsonStr := `{"a":{"b":[1,2,3]}}`

	val, err := walkJSONPath(jsonStr, "a.b[1]")
	if err != nil {
		t.Fatal(err)
	}
	if val.(float64) != 2 {
		t.Fatalf("expected 2, got %v", val)
	}
}

func TestParsePathPart(t *testing.T) {
	tests := []struct {
		part    string
		key     string
		indices []int
		wantErr bool
	}{
		{"name", "name", nil, false},
		{"name[0]", "name", []int{0}, false},
		{"a[1][2]", "a", []int{1, 2}, false},
		{"[0]", "", []int{0}, false},
		{"name[12", "", nil, true},
		{"a[1]x", "", nil, true},
		{"a[x]", "", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.part, func(t *testing.T) {
			key, indices, err := parsePathPart(tt.part)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.part)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.part, err)
			}
			if key != tt.key {
				t.Fatalf("key = %q, want %q", key, tt.key)
			}
			if len(indices) != len(tt.indices) {
				t.Fatalf("indices = %v, want %v", indices, tt.indices)
			}
			for i := range indices {
				if indices[i] != tt.indices[i] {
					t.Fatalf("indices = %v, want %v", indices, tt.indices)
				}
			}
		})
	}
}

func TestWalkJSONPathNested(t *testing.T) {
	jsonStr := `{"matrix":[[10,20],[30,40]]}`

	val, err := walkJSONPath(jsonStr, "matrix[1][0]")
	if err != nil {
		t.Fatal(err)
	}
	if val.(float64) != 30 {
		t.Fatalf("expected 30, got %v", val)
	}

	if _, err := walkJSONPath(jsonStr, "matrix[1"); err == nil {
		t.Fatal("expected error for malformed path")
	}
}

func TestConditionSetAND(t *testing.T) {
	raw := cs("and",
		group("and", Assertion{Type: "status_code", Operator: "eq", Value: "200"}),
		group("and", Assertion{Type: "body_contains", Operator: "contains", Value: "ok"}),
	)

	result := Evaluate(raw, 200, "ok", nil, 100, nil, nil)
	if !result.Pass {
		t.Fatal("expected pass when both groups pass")
	}

	result = Evaluate(raw, 200, "nope", nil, 100, nil, nil)
	if result.Pass {
		t.Fatal("expected fail when one group fails (AND)")
	}
}

func TestConditionSetOR(t *testing.T) {
	raw := cs("or",
		group("and", Assertion{Type: "status_code", Operator: "eq", Value: "200"}),
		group("and", Assertion{Type: "status_code", Operator: "eq", Value: "201"}),
	)

	result := Evaluate(raw, 200, "", nil, 100, nil, nil)
	if !result.Pass {
		t.Fatal("expected pass for 200 (OR)")
	}

	result = Evaluate(raw, 201, "", nil, 100, nil, nil)
	if !result.Pass {
		t.Fatal("expected pass for 201 (OR)")
	}

	result = Evaluate(raw, 500, "", nil, 100, nil, nil)
	if result.Pass {
		t.Fatal("expected fail for 500 (OR, neither group passes)")
	}
}

func TestConditionGroupOR(t *testing.T) {
	raw := cs("and", group("or",
		Assertion{Type: "status_code", Operator: "eq", Value: "200"},
		Assertion{Type: "status_code", Operator: "eq", Value: "201"},
	))

	result := Evaluate(raw, 200, "", nil, 100, nil, nil)
	if !result.Pass {
		t.Fatal("expected pass for 200 (inner OR)")
	}

	result = Evaluate(raw, 201, "", nil, 100, nil, nil)
	if !result.Pass {
		t.Fatal("expected pass for 201 (inner OR)")
	}

	result = Evaluate(raw, 500, "", nil, 100, nil, nil)
	if result.Pass {
		t.Fatal("expected fail for 500 (inner OR)")
	}
}

func TestConditionSetDegradedPropagation(t *testing.T) {
	raw := cs("and", group("and", Assertion{Type: "response_time", Operator: "lt", Value: "100", Degraded: true}))

	result := Evaluate(raw, 200, "", nil, 500, nil, nil)
	if result.Pass {
		t.Fatal("expected fail")
	}
	if !result.Degraded {
		t.Fatal("expected degraded=true")
	}
}

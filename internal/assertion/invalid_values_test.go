package assertion

import (
	"encoding/json"
	"testing"
)

func TestNumericAssertionsNeverTreatInvalidValuesAsZero(t *testing.T) {
	for _, value := range []string{"unavailable", "", "NaN", "Inf", "-Inf", "1e999"} {
		for _, condition := range []Assertion{
			{Type: "header", Target: "Load", Operator: "lte", Value: "10"},
			{Type: "json_path", Target: "load", Operator: "lte", Value: "10"},
			{Type: "header", Target: "Load", Operator: "gte", Value: value},
			{Type: "response_time", Operator: "gte", Value: value},
			{Type: "status_code", Operator: "gte", Value: value},
			{Type: "cert_expiry", Operator: "gte", Value: value},
		} {
			body, _ := json.Marshal(map[string]string{"load": value})
			expiry := _nowFunc().Unix() + 86400
			result := Evaluate(cs("and", group("and", condition)), 200, string(body), map[string]string{"Load": value}, 10, &expiry, nil)
			if result.Pass {
				t.Errorf("%s %s %q accepted invalid numeric data %q", condition.Type, condition.Operator, condition.Value, value)
			}
		}
	}
}

func TestMalformedAssertionConfigurationDoesNotReportSuccess(t *testing.T) {
	for _, raw := range []string{
		`{"groups":`, `{"unknown":true}`, `{"operator":"xor","groups":[]}`, `{"groups":[{"operator":"xor","conditions":[]}]}`,
		`{"operator":"or","groups":[{"operator":"xor"},{"conditions":[{"type":"status_code","value":"200"}]}]}`,
		`{"groups":[{"operator":"or","conditions":[{"type":"header","target":"Load","operator":"lt","value":"unavailable"},{"type":"status_code","value":"200"}]}]}`,
		`{"groups":[],"groups":[{"conditions":[{"type":"status_code","value":"200"}]}]}`,
		`{"groups":[{"conditions":[{"type":"status_code","value":"500","value":"200"}]}]}`,
		`{"GROUPS":[{"conditions":[{"type":"status_code","value":"500"}]}]}`,
	} {
		result := Evaluate(json.RawMessage(raw), 200, "", nil, 10, nil, nil)
		if result.Pass || result.Message == "" {
			t.Errorf("invalid assertion configuration reported success: %s", raw)
		}
	}
	for _, raw := range []string{"", "null", "[]", "{}", `{"groups":[]}`} {
		if !Evaluate(json.RawMessage(raw), 200, "", nil, 10, nil, nil).Pass {
			t.Errorf("empty legacy configuration rejected: %s", raw)
		}
	}
}

package assertion

import "testing"

func TestHardAssertionFailureDominatesSoftFailures(t *testing.T) {
	hardFail := Assertion{Type: "status_code", Operator: "eq", Value: "200"}
	softFail := Assertion{Type: "response_time", Operator: "lt", Value: "100", Degraded: true}
	passing := Assertion{Type: "status_code", Operator: "eq", Value: "500"}
	for _, tc := range []struct {
		name     string
		op       string
		groups   []ConditionGroup
		pass     bool
		degraded bool
	}{
		{"hard then soft", "and", []ConditionGroup{group("and", hardFail, softFail)}, false, false},
		{"soft then hard", "and", []ConditionGroup{group("and", softFail, hardFail)}, false, false},
		{"failed OR group", "and", []ConditionGroup{group("or", softFail, hardFail)}, false, false},
		{"separate failed groups", "and", []ConditionGroup{group("and", hardFail), group("and", softFail)}, false, false},
		{"reverse failed groups", "and", []ConditionGroup{group("and", softFail), group("and", hardFail)}, false, false},
		{"failed OR set", "or", []ConditionGroup{group("and", hardFail), group("and", softFail)}, false, false},
		{"soft failure only", "and", []ConditionGroup{group("and", passing, softFail)}, false, true},
		{"hard failure only", "and", []ConditionGroup{group("and", passing, hardFail)}, false, false},
		{"passing group alternative", "and", []ConditionGroup{group("or", passing, hardFail), group("and", softFail)}, false, true},
		{"passing set alternative", "or", []ConditionGroup{group("and", hardFail, softFail), group("and", passing)}, true, false},
		{"passing group", "and", []ConditionGroup{group("or", hardFail, softFail, passing)}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := Evaluate(cs(tc.op, tc.groups...), 500, "", nil, 200, nil, nil)
			if result.Pass != tc.pass || result.Degraded != tc.degraded {
				t.Fatalf("severity weakened or boolean structure ignored: %+v", result)
			}
		})
	}
}

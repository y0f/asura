package checker

import (
	"strings"
	"testing"
)

func TestInterpolateVars(t *testing.T) {
	vars := map[string]string{"token": "abc123", "id": "42"}

	tests := []struct {
		input string
		want  string
	}{
		{"no vars", "no vars"},
		{"Bearer {{token}}", "Bearer abc123"},
		{"/users/{{id}}/profile", "/users/42/profile"},
		{"{{token}} and {{id}}", "abc123 and 42"},
		{"{{missing}}", "{{missing}}"},
	}

	for _, tt := range tests {
		got, err := interpolateVars(tt.input, vars, maxMultiBody)
		if err != nil || got != tt.want {
			t.Errorf("interpolateVars(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestInterpolateVarsBoundariesAndLiteralValues(t *testing.T) {
	vars := map[string]string{"token": "abcd", "indirect": "{{token}}", "empty": ""}
	for _, tc := range []struct {
		input string
		limit int
		want  string
		fail  bool
	}{
		{"{{token}}{{token}}", 8, "abcdabcd", false},
		{"{{token}}{{token}}", 7, "", true},
		{"abc", 3, "abc", false},
		{"abc", 2, "", true},
		{"{{unknown}}", 11, "{{unknown}}", false},
		{"{{unknown}}", 10, "", true},
		{"{{unclosed", 10, "{{unclosed", false},
		{"{{unclosed", 9, "", true},
		{"{{empty}}", 0, "", false},
		{"", 0, "", false},
		{"{{indirect}}", 9, "{{token}}", false},
		{"{{token}}{{token}}{{token}}", 8, "", true},
	} {
		got, err := interpolateVars(tc.input, vars, tc.limit)
		if (err != nil) != tc.fail || got != tc.want {
			t.Errorf("input=%q limit=%d got=%q err=%v want=%q fail=%v", tc.input, tc.limit, got, err, tc.want, tc.fail)
		}
	}
	// This template would otherwise amplify a 1 MiB extraction to 1 GiB.
	// Rejection must not allocate/copy the expanded value before checking.
	got, err := interpolateVars(strings.Repeat("{{large}}", 1024), map[string]string{"large": strings.Repeat("x", 1<<20)}, 32)
	if err == nil || got != "" {
		t.Fatal("large expansion escaped the small output budget")
	}
}

func TestExtractVariable(t *testing.T) {
	body := `{"data":{"token":"secret123","count":5},"status":"ok"}`

	tests := []struct {
		name  string
		regex string
		json  string
		want  string
	}{
		{"json simple", "", "status", "ok"},
		{"json nested", "", "data.token", "secret123"},
		{"json number", "", "data.count", "5"},
		{"regex capture group", `"token":"([^"]+)"`, "", "secret123"},
		{"regex no group", `secret\d+`, "", "secret123"},
		{"json missing path", "", "data.missing", ""},
		{"empty both", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractVariable(body, tt.regex, tt.json)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractJSONPathNested(t *testing.T) {
	body := `{"auth":{"bearer":"tok_abc"}}`
	got := extractJSONPath(body, "auth.bearer")
	if got != "tok_abc" {
		t.Errorf("got %q, want tok_abc", got)
	}
}

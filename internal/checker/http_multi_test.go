package checker

import (
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
		got := interpolateVars(tt.input, vars)
		if got != tt.want {
			t.Errorf("interpolateVars(%q) = %q, want %q", tt.input, got, tt.want)
		}
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

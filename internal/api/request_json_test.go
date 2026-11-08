package api

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestJSONRequiresSingleDocumentWithoutLoggingInput(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		valid      bool
	}{
		{"object", `{"name":"valid"}`, true},
		{"whitespace", "{\"name\":\"valid\"} \r\n\t", true},
		{"second object", `{"name":"valid"}{"name":"private-value"}`, false},
		{"trailing null", `{"name":"valid"}null`, false},
		{"trailing token", `{"name":"valid"}private-value`, false},
		{"unknown field", `{"private-value":"secret"}`, false},
		{"wrong type", `{"name":123}`, false},
		{"empty", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			h := &Handler{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
			r := httptest.NewRequest("POST", "/private-value", strings.NewReader(tt.body))
			r.Pattern = "POST /example"
			var target struct {
				Name string `json:"name"`
			}
			err := h.readJSON(r, &target)
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v error=%v", tt.valid, err)
			}
			if strings.Contains(logs.String(), "private-value") || strings.Contains(logs.String(), "secret") {
				t.Fatalf("submitted content leaked to log: %s", logs.String())
			}
			if err != nil && strings.Contains(err.Error(), "private-value") {
				t.Fatal("submitted content leaked to response")
			}
		})
	}
}

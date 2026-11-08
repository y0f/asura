package checker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/y0f/asura/internal/model"
)

func TestHTTPMultiRejectsAmplifiedRequestBeforeSink(t *testing.T) {
	for _, field := range []string{"URL", "body", "header", "combined headers"} {
		t.Run(field, func(t *testing.T) {
			const privateMarker = "private-extracted-value"
			value := privateMarker + strings.Repeat("x", (32<<10)-len(privateMarker))
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]string{"token": value})
			}))
			defer source.Close()
			var received atomic.Int64
			sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusOK)
			}))
			defer sink.Close()
			step := model.HTTPStep{URL: sink.URL, Method: http.MethodPost}
			switch field {
			case "URL":
				step.URL += "/{{token}}"
			case "body":
				step.Body = strings.Repeat("{{token}}", 33)
			case "header":
				step.Headers = map[string]string{"X-Token": "{{token}}{{token}}"}
			case "combined headers":
				step.Headers = map[string]string{"X-First": "{{token}}", "X-Second": "{{token}}"}
			}
			settings, err := json.Marshal(model.MultiStepHTTPSettings{Steps: []model.HTTPStep{
				{URL: source.URL, ExtractVar: "token", ExtractJSON: "token"}, step,
			}})
			if err != nil {
				t.Fatal(err)
			}
			got, err := (&HTTPMultiChecker{AllowPrivate: true}).Check(context.Background(), &model.Monitor{Timeout: 2, Settings: settings})
			if err != nil || got == nil || got.Status != "down" || !strings.Contains(got.Message, "size limits") {
				t.Fatalf("oversized %s was not rejected safely: result=%+v error=%v", field, got, err)
			}
			if received.Load() != 0 {
				t.Fatalf("sink received %d rejected requests", received.Load())
			}
			if strings.Contains(got.Message, privateMarker) || strings.Contains(got.Message, sink.URL) {
				t.Fatal("limit error disclosed expanded request contents")
			}
		})
	}
}

func TestHTTPMultiRejectsOversizedSettingsBeforeDecode(t *testing.T) {
	// Deliberately invalid JSON proves the byte guard precedes decoder errors.
	settings := json.RawMessage(strings.Repeat("x", 1<<20+1))
	got, err := (&HTTPMultiChecker{}).Check(context.Background(), &model.Monitor{Settings: settings})
	if err != nil || got == nil || got.Status != "down" || got.Message != "multi-step settings exceed 1 MiB" {
		t.Fatalf("unexpected oversized-settings result: %+v %v", got, err)
	}
}

func TestHTTPMultiAcceptsExactResolvedRequestLimits(t *testing.T) {
	const urlLimit, bodyLimit, headerLimit = 8 << 10, 1 << 20, 64 << 10
	var received atomic.Int64
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) != bodyLimit || len(r.Header.Get("X-Token"))+len("X-Token") != headerLimit {
			t.Errorf("boundary request body/header mismatch: body=%d header=%d err=%v", len(body), len(r.Header.Get("X-Token")), err)
		}
		received.Add(1)
	}))
	defer sink.Close()
	// A 1 KiB extraction supports a compact body template exactly at 1 MiB.
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": strings.Repeat("x", 1024)})
	}))
	defer source.Close()
	settings, err := json.Marshal(model.MultiStepHTTPSettings{Steps: []model.HTTPStep{
		{URL: source.URL, ExtractVar: "token", ExtractJSON: "token"},
		{URL: sink.URL + "/" + strings.Repeat("{{token}}", 7) + strings.Repeat("u", urlLimit-len(sink.URL)-1-7*1024), Method: http.MethodPost,
			Body: strings.Repeat("{{token}}", 1024), Headers: map[string]string{"X-Token": strings.Repeat("h", headerLimit-len("X-Token"))}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := (&HTTPMultiChecker{AllowPrivate: true}).Check(context.Background(), &model.Monitor{Timeout: 2, Settings: settings})
	if err != nil || got == nil || got.Status != "up" || received.Load() != 1 {
		t.Fatalf("exact bounds were rejected: result=%+v err=%v received=%d", got, err, received.Load())
	}
}

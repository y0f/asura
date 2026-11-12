package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/y0f/asura/v3/internal/checker"
)

func TestAgentControlRedirectsDoNotForwardTokens(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			var redirected, leaked atomic.Int64
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				redirected.Add(1)
				if r.Header.Get("X-Agent-Token") == "fixture-private-token" {
					leaked.Add(1)
				}
				w.Write([]byte(`{"jobs":[],"accepted":1}`))
			}))
			defer destination.Close()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Agent-Token") != "fixture-private-token" {
					t.Error("origin did not receive token")
				}
				http.Redirect(w, r, destination.URL+"/capture", code)
			}))
			defer origin.Close()
			a := newAgent(origin.Client(), strings.Replace(origin.URL, "127.0.0.1", "localhost", 1), "fixture-private-token", checker.NewRegistry(), testLogger)
			if _, err := fetchJobs(context.Background(), a.client, a.server, a.token); err == nil {
				t.Error("redirected jobs request succeeded")
			}
			if err := postResults(context.Background(), a.client, a.server, a.token, []result{{MonitorID: 1, ConfigRevision: 1, Status: "up"}}); err == nil {
				t.Error("redirected results request succeeded")
			}
			if redirected.Load() != 0 || leaked.Load() != 0 {
				t.Errorf("redirected requests=%d, token disclosures=%d", redirected.Load(), leaked.Load())
			}
		})
	}
}

func TestAgentControlJobFailureDoesNotExposeResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("upstream echoed fixture-private-token"))
	}))
	defer server.Close()
	_, err := fetchJobs(context.Background(), server.Client(), server.URL, "fixture-private-token")
	if err == nil || !strings.Contains(err.Error(), "502") || strings.Contains(err.Error(), "fixture-private-token") {
		t.Fatalf("unsafe or missing failure: %v", err)
	}
}

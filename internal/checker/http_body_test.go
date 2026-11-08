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

	"github.com/y0f/asura/v2/internal/storage"
)

func TestHTTPCheckersRejectIncompleteBody(t *testing.T) {
	for _, kind := range []string{"http", "http_multi"} {
		for _, failure := range []string{"truncated", "timeout"} {
			t.Run(kind+"/"+failure, func(t *testing.T) {
				var nextStep atomic.Bool
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/next" {
						nextStep.Store(true)
						io.WriteString(w, "ok")
						return
					}
					w.Header().Set("Content-Length", "100")
					io.WriteString(w, "partial")
					if failure == "timeout" {
						w.(http.Flusher).Flush()
						<-r.Context().Done()
					}
				}))
				defer srv.Close()
				mon := &storage.Monitor{Target: srv.URL, Timeout: 1}
				var c Checker = &HTTPChecker{AllowPrivate: true}
				if kind == "http_multi" {
					c = &HTTPMultiChecker{AllowPrivate: true}
					settings, err := json.Marshal(storage.MultiStepHTTPSettings{Steps: []storage.HTTPStep{
						{Name: "login", URL: srv.URL, ExpectedStatus: 200, ExtractVar: "token", ExtractRegex: "(.+)"},
						{URL: srv.URL + "/next?token={{token}}"},
					}})
					if err != nil {
						t.Fatal(err)
					}
					mon.Settings = settings
				}
				got, err := c.Check(context.Background(), mon)
				if err != nil || got.Status != "down" || got.StatusCode != 200 {
					t.Fatalf("incomplete response = %+v, %v; want down with HTTP 200", got, err)
				}
				if !strings.Contains(got.Message, "body read failed") {
					t.Errorf("missing body failure diagnostic: %q", got.Message)
				}
				if kind == "http_multi" && !strings.Contains(got.Message, "login") {
					t.Errorf("missing failed step name: %q", got.Message)
				}
				if got.Body != "" || got.BodyHash != "" {
					t.Error("partial body must not be returned for content-change tracking")
				}
				if nextStep.Load() {
					t.Error("multistep check continued after an incomplete response")
				}
			})
		}
	}
}

func TestHTTPCheckersPreserveBodyLimit(t *testing.T) {
	body := strings.Repeat("x", maxBodyRead+100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()
	for _, kind := range []string{"http", "http_multi"} {
		t.Run(kind, func(t *testing.T) {
			mon := &storage.Monitor{Target: srv.URL, Timeout: 5}
			var c Checker = &HTTPChecker{AllowPrivate: true}
			if kind == "http_multi" {
				c = &HTTPMultiChecker{AllowPrivate: true}
				settings, err := json.Marshal(storage.MultiStepHTTPSettings{Steps: []storage.HTTPStep{{URL: srv.URL}}})
				if err != nil {
					t.Fatal(err)
				}
				mon.Settings = settings
			}
			got, err := c.Check(context.Background(), mon)
			if err != nil || got.Status != "up" {
				t.Fatalf("large response = %+v, %v; want up", got, err)
			}
			if kind == "http" && (len(got.Body) != maxBodyRead || got.BodyHash == "") {
				t.Errorf("inspected body has %d bytes, hash=%q", len(got.Body), got.BodyHash)
			}
		})
	}
}

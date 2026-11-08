package server

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/storage"
)

func TestReviewRequestLogsDoNotRetainHeartbeatCredentials(t *testing.T) {
	const secret = "fixture-private-heartbeat-token"
	for _, base := range []string{"", "/asura"} {
		for _, outcome := range []string{"success", "rejection", "panic"} {
			t.Run(base+"/"+outcome, func(t *testing.T) {
				var output bytes.Buffer
				logger := slog.New(slog.NewTextHandler(&output, nil))
				writer := NewRequestLogWriter(nil, logger)
				var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if outcome == "panic" {
						panic("controlled fixture panic: " + secret)
					}
					status := http.StatusNoContent
					if outcome == "rejection" {
						status = http.StatusNotFound
					}
					w.WriteHeader(status)
				})
				handler = requestLogMiddleware(writer, base, nil)(handler)
				handler = logging(logger)(handler)
				handler = recovery(logger)(handler)
				r := httptest.NewRequest(http.MethodPost, base+"/api/v1/heartbeat/"+secret, nil)
				handler.ServeHTTP(httptest.NewRecorder(), r)
				if strings.Contains(output.String(), secret) {
					t.Error("process log retained reusable heartbeat credential")
				}
				select {
				case entry := <-writer.ch:
					if strings.Contains(entry.Path, secret) {
						t.Error("persisted request-log payload retained reusable heartbeat credential")
					}
				default:
					if outcome != "panic" {
						t.Fatal("ordinary request accounting disappeared")
					}
				}
			})
		}
	}
}

func TestReviewRequestRefererDoesNotRetainURLCredentials(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	writer := NewRequestLogWriter(nil, logger)
	handler := requestLogMiddleware(writer, "", nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	r := httptest.NewRequest(http.MethodGet, "/monitors", nil)
	r.Header.Set("Referer", "https://fixture-user:fixture-password@asura.example/status/confirm?token=fixture-confirm-token#fixture-fragment")
	handler.ServeHTTP(httptest.NewRecorder(), r)
	entry := <-writer.ch
	for _, secret := range []string{"fixture-password", "fixture-confirm-token", "fixture-fragment"} {
		if strings.Contains(entry.Referer, secret) {
			t.Errorf("request-log referer retained %s", secret)
		}
	}
}

func TestRequestCredentialProjectionHandlesEncodedAndMalformedPaths(t *testing.T) {
	for _, path := range []string{
		"/asura/api/v1/heartbeat/private-token",
		"/asura/api/v1/heartbeat%2Fprivate-token",
		"/asura/%61pi/v1/heartbeat/private-token",
		"/asura//api/./v1/heartbeat/private-token",
		"/asura/api/./v1/heartbeat/private-token/../../private-token",
	} {
		t.Run(path, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&output, nil))
			writer := NewRequestLogWriter(nil, logger)
			handler := logging(logger)(requestLogMiddleware(writer, "/asura", nil)(http.NotFoundHandler()))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, path, nil))
			entry := <-writer.ch
			if strings.Contains(output.String(), "private-token") || strings.Contains(entry.Path, "private-token") || entry.Path != "/asura/api/v1/heartbeat/[redacted]" {
				t.Fatal("rejected path exposed a credential or disappeared from accounting")
			}
		})
	}
}

type credentialRouteStore struct {
	storage.Store
	seen string
}

func (*credentialRouteStore) ListStatusPages(context.Context) ([]*storage.StatusPage, error) {
	return nil, nil
}

func (s *credentialRouteStore) GetHeartbeatByToken(_ context.Context, token string) (*storage.Heartbeat, error) {
	s.seen = token
	return nil, sql.ErrNoRows
}

func TestRequestCredentialProjectionThroughActualRouter(t *testing.T) {
	for _, base := range []string{"", "/asura"} {
		t.Run(base, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&output, nil))
			cfg := config.Defaults()
			cfg.Server.BasePath = base
			store := &credentialRouteStore{}
			server := NewServer(cfg, store, nil, nil, nil, logger, "test")
			t.Cleanup(server.Close)
			r := httptest.NewRequest(http.MethodGet, base+"/%61pi/v1/heartbeat/private%2Dtoken", nil)
			r.Header.Set("Referer", "https://example.test/services/unsubscribe?token=private-subscription-token")
			w := httptest.NewRecorder()
			server.ServeHTTP(w, r)
			if w.Code != http.StatusNotFound || store.seen != "private-token" {
				t.Fatalf("encoded request did not reach the actual heartbeat handler: status=%d token=%q", w.Code, store.seen)
			}
			entry := <-server.reqLogWriter.ch
			if strings.Contains(output.String(), "private") || entry.Path != base+"/api/v1/heartbeat/[redacted]" || entry.Referer != "https://example.test/services/unsubscribe" {
				t.Fatal("actual routing exposed request credentials")
			}
		})
	}
}

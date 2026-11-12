package api_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/y0f/asura/v3/internal/api"
	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/storage"
	"github.com/y0f/asura/v3/internal/web"
)

type pageInterleaveStore struct {
	storage.Store
	change func()
}

func TestPublicSubscriptionNeverUsesStalePageAuthorization(t *testing.T) {
	for _, endpoint := range []string{"api", "web"} {
		t.Run(endpoint, func(t *testing.T) {
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "subscription.db"), 2)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { store.Close() })
			ctx := context.Background()
			page := &storage.StatusPage{Slug: "subscribe", Enabled: true, APIEnabled: true}
			if err := store.CreateStatusPage(ctx, page); err != nil {
				t.Fatal(err)
			}
			wrapped := &pageInterleaveStore{Store: store, change: func() {
				next := *page
				next.PasswordHash = config.HashAPIKey("now-private")
				if err := store.UpdateStatusPage(ctx, &next); err != nil {
					t.Fatal(err)
				}
			}}
			cfg := config.Defaults()
			cfg.Monitor.AllowPrivateTargets = true
			cfg.Subscriptions.Enabled = true
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			body := `{"type":"webhook","webhook_url":"https://127.0.0.1/subscriber-fixture"}`
			contentType := "application/json"
			if endpoint == "web" {
				body = url.Values{"type": {"webhook"}, "webhook_url": {"https://127.0.0.1/subscriber-fixture"}}.Encode()
				contentType = "application/x-www-form-urlencoded"
			}
			r := httptest.NewRequest(http.MethodPost, "/subscribe", strings.NewReader(body))
			r.Header.Set("Content-Type", contentType)
			r.SetPathValue("id", fmt.Sprint(page.ID))
			w := httptest.NewRecorder()
			limiter := httputil.NewRateLimiter(100, 100)
			t.Cleanup(limiter.Stop)
			if endpoint == "api" {
				api.New(cfg, wrapped, nil, nil, logger, limiter).SubscribeAPI(w, r)
			} else {
				h := web.New(cfg, wrapped, nil, nil, nil, logger, "test", "frame-ancestors 'none'", limiter)
				t.Cleanup(h.Stop)
				h.StatusPageSubscribe(w, r, page.ID)
			}
			count, err := store.CountSubscribersByPage(ctx, page.ID)
			if w.Code != http.StatusNotFound || err != nil || count != 0 {
				t.Fatalf("stale public admission survived: status=%d count=%d err=%v", w.Code, count, err)
			}
		})
	}
}

type subscriberFailureStore struct{ storage.Store }

func (*subscriberFailureStore) ConfirmPageSubscriber(context.Context, int64, string) error {
	return errors.New("injected confirmation failure")
}
func (*subscriberFailureStore) DeletePageSubscriberByToken(context.Context, int64, string) error {
	return errors.New("injected unsubscribe failure")
}

func TestSubscriberTokenFailuresNeverClaimSuccess(t *testing.T) {
	for _, endpoint := range []string{"api", "web"} {
		t.Run(endpoint, func(t *testing.T) {
			cfg := config.Defaults()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			store := &subscriberFailureStore{}
			apiHandler := api.New(cfg, store, nil, nil, logger, nil)
			webHandler := web.New(cfg, store, nil, nil, nil, logger, "test", "frame-ancestors 'none'", nil)
			t.Cleanup(webHandler.Stop)
			for _, confirm := range []bool{false, true} {
				r := httptest.NewRequest(http.MethodGet, "/services/unsubscribe?token=private-fixture-token", nil)
				w := httptest.NewRecorder()
				if endpoint == "api" {
					if confirm {
						apiHandler.ConfirmSubscription(w, r, 1)
					} else {
						apiHandler.Unsubscribe(w, r, 1)
					}
				} else {
					if confirm {
						webHandler.StatusPageConfirm(w, r, 1)
					} else {
						webHandler.StatusPageUnsubscribe(w, r, 1)
					}
				}
				if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "private-fixture-token") {
					t.Fatalf("token failure reported success or leaked token: %d %s", w.Code, w.Body.String())
				}
			}
		})
	}
}

func (s *pageInterleaveStore) GetStatusPage(ctx context.Context, id int64) (*storage.StatusPage, error) {
	page, err := s.Store.GetStatusPage(ctx, id)
	if err == nil && s.change != nil {
		change := s.change
		s.change = nil
		change()
	}
	return page, err
}

func TestPublicPageNeverMixesOldAuthorityWithPrivateMembership(t *testing.T) {
	for _, endpoint := range []string{"api", "web", "RSS"} {
		t.Run(endpoint, func(t *testing.T) {
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "review.db"), 2)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { store.Close() })
			ctx := context.Background()
			private := &storage.Monitor{Name: "unpublished-private-monitor-fixture", Type: "http", Target: "https://private.example", Enabled: true, Interval: 60, Timeout: 10}
			if err := store.CreateMonitor(ctx, private); err != nil {
				t.Fatal(err)
			}
			page := &storage.StatusPage{Slug: "services", Title: "Services", Enabled: true, APIEnabled: true, ShowIncidents: true}
			if err := store.CreateStatusPage(ctx, page); err != nil {
				t.Fatal(err)
			}
			wrapped := &pageInterleaveStore{Store: store}
			wrapped.change = func() {
				_, err := store.SaveStatusPage(ctx, page.ID, func(current *storage.StatusPage) (*storage.StatusPage, *[]storage.StatusPageMonitor, error) {
					next := *current
					next.PasswordHash = config.HashAPIKey("new-private-page-password")
					members := []storage.StatusPageMonitor{{MonitorID: private.ID}}
					return &next, &members, nil
				}, storage.StatusPageAccess{Monitors: []storage.MonitorAccess{{All: true}}, Incidents: []storage.MonitorAccess{{All: true}}})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.CreateIncident(ctx, &storage.Incident{MonitorID: private.ID, Status: "open"}); err != nil {
					t.Fatal(err)
				}
			}
			cfg := config.Defaults()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			r := httptest.NewRequest(http.MethodGet, "/services", nil)
			r.SetPathValue("id", fmt.Sprint(page.ID))
			w := httptest.NewRecorder()
			if endpoint == "api" {
				h := api.New(cfg, wrapped, nil, nil, logger, nil)
				h.PublicStatusPage(w, r)
			} else {
				h := web.New(cfg, wrapped, nil, nil, nil, logger, "test", "frame-ancestors 'none'", nil)
				t.Cleanup(h.Stop)
				if endpoint == "RSS" {
					h.StatusPageRSS(w, r, page.ID)
				} else {
					h.StatusPageByID(w, r, page.ID)
				}
			}
			if strings.Contains(w.Body.String(), private.Name) {
				t.Fatalf("anonymous response mixed old public authorization with new protected membership: status=%d", w.Code)
			}
			if w.Code != http.StatusNotFound {
				t.Fatalf("changed page protection was not rejected: status=%d", w.Code)
			}
		})
	}
}

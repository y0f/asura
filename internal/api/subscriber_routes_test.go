package api_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/config"
	"github.com/y0f/asura/v2/internal/storage"
)

func TestSubscriberRoutesKeepTokenScopeAndDisabledUnsubscribe(t *testing.T) {
	for _, noWeb := range []bool{false, true} {
		for _, base := range []string{"", "/asura"} {
			t.Run(map[bool]string{false: "web", true: "API"}[noWeb]+base, func(t *testing.T) {
				env := newTestEnv(t, func(c *config.Config) {
					c.Server.BasePath = base
					if noWeb {
						headless(c)
					}
				}, false, nil)
				ctx := context.Background()
				page := &storage.StatusPage{Slug: "disabled", Title: "private-page-branding", PasswordHash: config.HashAPIKey("page-password")}
				other := &storage.StatusPage{Slug: "other", Title: "other-private-branding", Enabled: true}
				for _, p := range []*storage.StatusPage{page, other} {
					if err := env.store.CreateStatusPage(ctx, p); err != nil {
						t.Fatal(err)
					}
				}
				sub := &storage.StatusPageSubscriber{StatusPageID: page.ID, Type: "email", Email: "fixture@example.test", Confirmed: true}
				if err := env.store.CreateStatusPageSubscriber(ctx, sub); err != nil {
					t.Fatal(err)
				}
				wrong := env.do(http.MethodGet, base+"/other/unsubscribe?token="+sub.Token, "", nil)
				if wrong.Code != http.StatusOK || strings.Contains(wrong.Body.String(), other.Title) {
					t.Fatalf("wrong-page result exposed branding or failed idempotence: %d", wrong.Code)
				}
				if _, err := env.store.GetSubscriberByToken(ctx, sub.Token); err != nil {
					t.Fatalf("another page removed subscriber: %v", err)
				}
				for range 2 {
					w := env.do(http.MethodGet, base+"/disabled/unsubscribe?token="+sub.Token, "", nil)
					if w.Code != http.StatusOK || strings.Contains(w.Body.String(), page.Title) || strings.Contains(w.Body.String(), sub.Token) {
						t.Fatalf("disabled unsubscribe failed or exposed branding/token: %d", w.Code)
					}
					if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
						t.Fatalf("token result lacks privacy headers: %v", w.Header())
					}
				}
				if _, err := env.store.GetSubscriberByToken(ctx, sub.Token); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("disabled unsubscribe did not remove row: %v", err)
				}
			})
		}
	}
}

func TestSubscriptionDisabledRejectsRegistrationAndKeepsExistingUnsubscribe(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) { c.Subscriptions.Enabled = false; c.Monitor.AllowPrivateTargets = true }, false, nil)
	page := env.createStatusPage(t, `{"slug":"disabled-subs","title":"Subscriptions","enabled":true,"api_enabled":true}`)
	ctx := context.Background()
	existing := &storage.StatusPageSubscriber{StatusPageID: page.ID, Type: "webhook", WebhookURL: "https://127.0.0.1/existing", Confirmed: true}
	if err := env.store.CreateStatusPageSubscriber(ctx, existing); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"email", "webhook"} {
		for _, webForm := range []bool{false, true} {
			path := "/api/v1/status-pages/" + fmt.Sprint(page.ID) + "/subscribe"
			body := `{"type":"` + kind + `","email":"fixture@example.test","webhook_url":"https://127.0.0.1/new"}`
			var headers map[string]string
			if webForm {
				path = "/disabled-subs/subscribe"
				body = url.Values{"type": {kind}, "email": {"fixture@example.test"}, "webhook_url": {"https://127.0.0.1/new"}}.Encode()
				headers = map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
			}
			w := env.do(http.MethodPost, path, body, headers)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("disabled registration accepted: kind=%s web=%v status=%d", kind, webForm, w.Code)
			}
		}
	}
	count, err := env.store.CountSubscribersByPage(ctx, page.ID)
	if err != nil || count != 1 {
		t.Fatalf("disabled admission changed existing subscriptions: %d %v", count, err)
	}
	w := env.do(http.MethodGet, "/disabled-subs/unsubscribe?token="+existing.Token, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("global disable blocked unsubscribe: %d", w.Code)
	}
	if _, err := env.store.GetSubscriberByToken(ctx, existing.Token); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("existing subscription not removed: %v", err)
	}
}

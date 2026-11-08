package web

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/config"
	"github.com/y0f/asura/v2/internal/httputil"
	"github.com/y0f/asura/v2/internal/notifier"
	"github.com/y0f/asura/v2/internal/storage"
)

func TestWebSubscriptionRejectedQueueAllowsRetry(t *testing.T) {
	h, s := storeHandler(t)
	h.cfg.Subscriptions.Enabled = true
	h.loginRL = httputil.NewRateLimiter(100, 100)
	t.Cleanup(h.loginRL.Stop)
	sp, _ := createProtectedStatusPage(t, s, false)
	h.subNotifier = notifier.NewSubscriberNotifier(s, config.SMTPConfig{Host: "unused.example", From: "test@example.test"}, "", h.logger, false)
	h.subNotifier.Shutdown(context.Background())
	for range 2 {
		form := url.Values{"type": {"email"}, "email": {"retry@example.test"}}
		r := httptest.NewRequest("POST", "/internal/subscribe", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.StatusPageSubscribe(w, r, sp.ID)
		if location := w.Header().Get("Location"); !strings.Contains(location, "Confirmation+email+unavailable") || strings.Contains(location, "Check+your+email") {
			t.Fatalf("wrong queue rejection redirect: %s", location)
		}
	}
	if err := s.CreateStatusPageSubscriber(context.Background(), &storage.StatusPageSubscriber{StatusPageID: sp.ID, Type: "email", Email: "retry@example.test"}); err != nil {
		t.Fatal(err)
	}
}

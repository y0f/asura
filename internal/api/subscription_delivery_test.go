package api_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/notifier"
	"github.com/y0f/asura/internal/storage"
)

func TestSubscriptionRejectedQueueDoesNotClaimEmailSentOrBlockRetry(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) { headless(c); c.Subscriptions.Enabled = true }, false, func(s storage.Store, c *config.Config) *notifier.SubscriberNotifier {
		n := notifier.NewSubscriberNotifier(s, config.SMTPConfig{Host: "unused.example", From: "test@example.test"}, "", slog.New(slog.NewTextHandler(io.Discard, nil)), false)
		n.Shutdown(context.Background())
		return n
	})
	sp := env.createStatusPage(t, `{"slug":"rejected","title":"Rejected","enabled":true,"api_enabled":true}`)
	for range 2 {
		w := env.do(http.MethodPost, fmt.Sprintf("/api/v1/status-pages/%d/subscribe", sp.ID), `{"type":"email","email":"retry@example.test"}`, nil)
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "try again") || strings.Contains(w.Body.String(), "sent") {
			t.Fatalf("rejected confirmation gave wrong response or blocked retry: %d %s", w.Code, w.Body.String())
		}
	}
	if err := env.store.CreateStatusPageSubscriber(context.Background(), &storage.StatusPageSubscriber{StatusPageID: sp.ID, Type: "email", Email: "retry@example.test"}); err != nil {
		t.Fatalf("queue rejection left an unusable subscriber row: %v", err)
	}
}

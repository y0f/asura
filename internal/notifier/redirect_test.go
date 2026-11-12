package notifier

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/y0f/asura/v3/internal/storage"
)

func TestGotifyRedirectDoesNotForwardCredentials(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			var redirected, leaked atomic.Int64
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				redirected.Add(1)
				if r.Header.Get("X-Gotify-Key") == "fixture-private-key" {
					leaked.Add(1)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer destination.Close()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Gotify-Key") != "fixture-private-key" {
					t.Error("origin did not receive credentials")
				}
				http.Redirect(w, r, destination.URL+"/other", code)
			}))
			defer origin.Close()
			// Different hostnames exercise custom-header forwarding across origins.
			settings, err := json.Marshal(GotifySettings{ServerURL: strings.Replace(origin.URL, "127.0.0.1", "localhost", 1), AppToken: "fixture-private-key", Priority: 5})
			if err != nil {
				t.Fatal(err)
			}
			sender := &GotifySender{AllowPrivate: true}
			err = sender.Send(context.Background(), &storage.NotificationChannel{Settings: settings}, &Payload{EventType: "test"})
			if err == nil {
				t.Error("redirect was accepted as successful delivery")
			}
			if redirected.Load() != 0 || leaked.Load() != 0 {
				t.Errorf("redirected requests=%d, credential disclosures=%d", redirected.Load(), leaked.Load())
			}
		})
	}
}

func TestNotificationHTTPRequiresSuccessfulStatus(t *testing.T) {
	for _, code := range []int{200, 202, 204, 300, 301, 302, 303, 304, 307, 308, 400, 500} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code) // Redirect statuses intentionally lack Location.
			}))
			defer server.Close()
			gotify, _ := json.Marshal(GotifySettings{ServerURL: server.URL, AppToken: "fixture-token"})
			webhook, _ := json.Marshal(WebhookSettings{URL: server.URL})
			payload := &Payload{EventType: "test"}
			subscriber := &SubscriberNotifier{allowPrivate: true}
			for _, tc := range []struct {
				name string
				send func() error
			}{
				{"gotify", func() error {
					return (&GotifySender{AllowPrivate: true}).Send(context.Background(), &storage.NotificationChannel{Settings: gotify}, payload)
				}},
				{"webhook", func() error {
					return (&WebhookSender{AllowPrivate: true}).Send(context.Background(), &storage.NotificationChannel{Settings: webhook}, payload)
				}},
				{"subscriber", func() error {
					return subscriber.sendSubscriberWebhook(context.Background(), &storage.StatusPageSubscriber{WebhookURL: server.URL}, &storage.StatusPage{}, "test", nil, nil)
				}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if err := tc.send(); (err == nil) != (code >= 200 && code < 300) {
						t.Errorf("HTTP %d delivery result: %v", code, err)
					}
				})
			}
		})
	}
}

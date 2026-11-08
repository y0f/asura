package notifier

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/y0f/asura/v2/internal/storage"
)

func TestGotifyPriorityDelivery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		priority any
		want     int
	}{
		{"omitted", nil, 5},
		{"zero", 0, 0},
		{"custom", 7, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			received := make(chan int, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/message" || r.Header.Get("X-Gotify-Key") != "fixture-token" {
					t.Error("unexpected Gotify request")
				}
				var body struct {
					Priority *int `json:"priority"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Priority == nil {
					t.Error("missing or invalid priority")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				received <- *body.Priority
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			settings := map[string]any{"server_url": server.URL, "app_token": "fixture-token"}
			if tc.priority != nil {
				settings["priority"] = tc.priority
			}
			raw, err := json.Marshal(settings)
			if err != nil {
				t.Fatal(err)
			}
			sender := &GotifySender{AllowPrivate: true}
			if err := sender.Send(context.Background(), &storage.NotificationChannel{Settings: raw}, &Payload{EventType: "test"}); err != nil {
				t.Fatal(err)
			}
			if got := <-received; got != tc.want {
				t.Fatalf("delivered priority %d, want %d", got, tc.want)
			}
		})
	}
}

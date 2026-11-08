package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/y0f/asura/v2/internal/storage"
)

func TestNotificationCreateEnabled(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		want        bool
	}{
		{"omitted defaults enabled", "", true},
		{"explicit true", `,"enabled":true`, true},
		{"explicit false", `,"enabled":false`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, nil, false, nil)
			w := env.do("POST", "/api/v1/notifications", `{"name":"channel","type":"webhook","settings":{"url":"https://example.com/hook"}`+tc.field+`}`, admin)
			if w.Code != http.StatusCreated {
				t.Fatalf("create: %d %s", w.Code, w.Body.String())
			}
			var created storage.NotificationChannel
			if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			stored, err := env.store.GetNotificationChannel(context.Background(), created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if created.Enabled != tc.want || stored.Enabled != tc.want {
				t.Fatalf("response/stored enabled = %t/%t, want %t", created.Enabled, stored.Enabled, tc.want)
			}
		})
	}
}

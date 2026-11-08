package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
)

func TestNotificationFormRetargetAndWriteOnlyRedirect(t *testing.T) {
	h, store := storeHandler(t)
	h.cfg.Server.BasePath = "/console"
	ch := &storage.NotificationChannel{Name: "private-channel", Type: "matrix", Enabled: true, Settings: json.RawMessage(`{"homeserver":"https://matrix.test","access_token":"saved-secret","room_id":"!room:test"}`)}
	if err := store.CreateNotificationChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	key := &config.APIKeyConfig{Name: "writer", Permissions: []string{"notifications.update", "incidents.read"}}
	update := func(settings string) *httptest.ResponseRecorder {
		t.Helper()
		form := url.Values{"name": {"renamed"}, "type": {"matrix"}, "enabled": {"on"}, "event_mode": {"all"}, "notif_settings_mode": {"json"}, "settings_json": {settings}}
		r := adminRequest("POST", "/console/notifications/"+strconvI(ch.ID), form)
		r.SetPathValue("id", strconvI(ch.ID))
		r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, key))
		w := httptest.NewRecorder()
		h.NotificationUpdate(w, r)
		return w
	}
	w := update(`{"homeserver":"https://other.test","access_token":"","room_id":"!room:test"}`)
	if w.Code != 303 || w.Header().Get("Location") != "/console/incidents" || strings.Contains(w.Header().Get("Set-Cookie"), "saved-secret") {
		t.Fatalf("write-only failed edit redirect: %d %v", w.Code, w.Header())
	}
	got, err := store.GetNotificationChannel(context.Background(), ch.ID)
	if err != nil || strings.Contains(string(got.Settings), "other.test") || got.Name != ch.Name {
		t.Fatal("denied credential retarget changed the channel")
	}
	w = update(`{"homeserver":"https://matrix.test","access_token":"","room_id":"!room:test"}`)
	got, err = store.GetNotificationChannel(context.Background(), ch.ID)
	if w.Code != 303 || err != nil || got.Name != "renamed" || !strings.Contains(string(got.Settings), "saved-secret") {
		t.Fatal("metadata edit lost its credential or failed")
	}
}

func TestNotificationReadAndDeleteFailuresReturnErrors(t *testing.T) {
	h, store := storeHandler(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, handler := range []http.HandlerFunc{h.Notifications, h.NotificationDelete, h.NotificationTest} {
		r := adminRequest("GET", "/notifications", nil)
		r.SetPathValue("id", "1")
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != 500 || strings.Contains(w.Header().Get("Set-Cookie"), "deleted") {
			t.Fatalf("failed channel query reported success: %d", w.Code)
		}
	}
}

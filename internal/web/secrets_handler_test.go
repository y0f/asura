package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
)

// storeHandler returns a Handler backed by a real temporary SQLite store.
func storeHandler(t *testing.T) (*Handler, *storage.SQLiteStore) {
	t.Helper()
	f, err := os.CreateTemp("", "asura-web-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })
	store, err := storage.NewSQLiteStore(f.Name(), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	h := testWebHandler(t)
	h.store = store
	return h, store
}

func adminRequest(method, target string, form url.Values) *http.Request {
	var r *http.Request
	if form != nil {
		r = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	key := &config.APIKeyConfig{Name: "admin", Role: "admin"}
	return r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, key))
}

func httpMonitorForm(token string, extra url.Values) url.Values {
	f := url.Values{
		"name": {"API"}, "type": {"http"}, "target": {"https://example.com"},
		"interval": {"60"}, "timeout": {"10"}, "failure_threshold": {"1"}, "success_threshold": {"1"},
		"settings_mode": {"form"}, "assertions_mode": {"form"},
		"settings_auth_method": {"bearer"}, "settings_bearer_token": {token},
	}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

func storedBearer(t *testing.T, store *storage.SQLiteStore, id int64) string {
	t.Helper()
	m, err := store.GetMonitor(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var s storage.HTTPSettings
	json.Unmarshal(m.Settings, &s)
	return s.BearerToken
}

func TestMonitorCreateErrorKeepsTypedSecret(t *testing.T) {
	h, _ := storeHandler(t)
	form := httpMonitorForm("typed-token", url.Values{"target": {""}}) // invalid: target required
	w := httptest.NewRecorder()
	h.MonitorCreate(w, adminRequest("POST", "/monitors", form))
	body := w.Body.String()
	if !strings.Contains(body, `value="typed-token"`) {
		t.Fatal("re-shown create form lost the typed token")
	}
	if strings.Contains(body, "Saved. Leave blank") {
		t.Fatal("create form claims a secret is saved when nothing was stored")
	}
}

func TestMonitorUpdateSecretKeepReplaceClear(t *testing.T) {
	h, store := storeHandler(t)
	mon := &storage.Monitor{Name: "API", Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10, Enabled: true,
		FailureThreshold: 1, SuccessThreshold: 1, Settings: json.RawMessage(`{"auth_method":"bearer","bearer_token":"stored-token"}`)}
	if err := store.CreateMonitor(context.Background(), mon); err != nil {
		t.Fatal(err)
	}
	id := strconvI(mon.ID)

	// The edit page never contains the stored token.
	w := httptest.NewRecorder()
	r := adminRequest("GET", "/monitors/"+id+"/edit", nil)
	r.SetPathValue("id", id)
	h.MonitorForm(w, r)
	if strings.Contains(w.Body.String(), "stored-token") {
		t.Fatal("edit page exposes the stored token")
	}
	if !strings.Contains(w.Body.String(), "Saved. Leave blank") {
		t.Fatal("edit page does not say the token is saved")
	}

	update := func(form url.Values) {
		t.Helper()
		w := httptest.NewRecorder()
		r := adminRequest("POST", "/monitors/"+id, form)
		r.SetPathValue("id", id)
		h.MonitorUpdate(w, r)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("update returned %d: %s", w.Code, w.Body.String())
		}
	}

	update(httpMonitorForm("", nil))
	if got := storedBearer(t, store, mon.ID); got != "stored-token" {
		t.Fatalf("blank field should keep the token, got %q", got)
	}
	update(httpMonitorForm("new-token", nil))
	if got := storedBearer(t, store, mon.ID); got != "new-token" {
		t.Fatalf("typed token should replace it, got %q", got)
	}
	update(httpMonitorForm("", url.Values{"clear_secrets": {"bearer_token"}}))
	if got := storedBearer(t, store, mon.ID); got != "" {
		t.Fatalf("remove saved value should clear it, got %q", got)
	}
}

func TestMonitorUpdateErrorDoesNotRenderStoredSecret(t *testing.T) {
	h, store := storeHandler(t)
	mon := &storage.Monitor{Name: "API", Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10, Enabled: true,
		FailureThreshold: 1, SuccessThreshold: 1, Settings: json.RawMessage(`{"auth_method":"bearer","bearer_token":"stored-token"}`)}
	if err := store.CreateMonitor(context.Background(), mon); err != nil {
		t.Fatal(err)
	}
	id := strconvI(mon.ID)
	w := httptest.NewRecorder()
	r := adminRequest("POST", "/monitors/"+id, httpMonitorForm("", url.Values{"target": {""}}))
	r.SetPathValue("id", id)
	h.MonitorUpdate(w, r)
	if strings.Contains(w.Body.String(), "stored-token") {
		t.Fatal("validation error page exposes the stored token")
	}
}

func TestNotificationTypeChangeRequiresNewSecret(t *testing.T) {
	h, store := storeHandler(t)
	ch := &storage.NotificationChannel{Name: "Ops", Type: "slack", Enabled: true,
		Settings: json.RawMessage(`{"webhook_url":"https://hooks.slack.com/x"}`), Events: []string{"incident.created"}}
	if err := store.CreateNotificationChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	id := strconvI(ch.ID)
	form := url.Values{"name": {"Ops"}, "type": {"discord"}, "enabled": {"on"}, "notif_settings_mode": {"form"},
		"notif_discord_webhook_url": {""}, "event_incident_created": {"on"}}
	w := httptest.NewRecorder()
	r := adminRequest("POST", "/notifications/"+id, form)
	r.SetPathValue("id", id)
	h.NotificationUpdate(w, r)
	got, _ := store.GetNotificationChannel(context.Background(), ch.ID)
	if got.Type != "slack" {
		t.Fatalf("channel saved as %s without a webhook URL", got.Type)
	}
}

func TestAgentCreateRedirectsAndRevealsOnce(t *testing.T) {
	h, _ := storeHandler(t)
	w := httptest.NewRecorder()
	h.AgentCreate(w, adminRequest("POST", "/agents", url.Values{"name": {"eu"}}))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("create should redirect, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	u, _ := url.Parse(loc)
	created := u.Query().Get("created")
	if created == "" {
		t.Fatalf("redirect has no reveal id: %s", loc)
	}

	w = httptest.NewRecorder()
	h.Agents(w, adminRequest("GET", loc, nil))
	if !strings.Contains(w.Body.String(), "Copy the token now") {
		t.Fatal("token panel not shown after create")
	}
	w = httptest.NewRecorder()
	h.Agents(w, adminRequest("GET", loc, nil))
	if strings.Contains(w.Body.String(), "Copy the token now") {
		t.Fatal("token shown a second time on refresh")
	}
}

func strconvI(v int64) string { return strconv.FormatInt(v, 10) }

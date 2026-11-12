package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/storage"
)

func TestMonitorPrivateFieldsStayOutOfAllWebReadSurfaces(t *testing.T) {
	h, store := storeHandler(t)
	ctx := context.Background()
	group := &storage.MonitorGroup{Name: "public group"}
	if err := store.CreateMonitorGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	monitor := &storage.Monitor{Name: "public name", Type: "http", Target: "https://private-user:private-pass@example.test/private-path?token=private-query", Interval: 60, Timeout: 10, Enabled: true, FailureThreshold: 1, SuccessThreshold: 1, GroupID: &group.ID,
		Settings: json.RawMessage(`{"method":"POST","body":"private-body","bearer_token":"private-token","headers":{"X-Token":"private-header"},"oauth2_token_url":"https://auth.test/private-path?key=private-query"}`)}
	if err := store.CreateMonitor(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path    string
		id      int64
		handler http.HandlerFunc
	}{
		{"/", 0, h.Dashboard}, {"/monitors", 0, h.Monitors},
		{"/groups/" + strconvI(group.ID), group.ID, h.GroupDetail},
		{"/monitors/" + strconvI(monitor.ID), monitor.ID, h.MonitorDetail},
		{"/monitors/" + strconvI(monitor.ID) + "/edit", monitor.ID, h.MonitorForm},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := adminRequest("GET", tc.path, nil)
			r.SetPathValue("id", strconvI(tc.id))
			w := httptest.NewRecorder()
			tc.handler(w, r)
			if w.Code != 200 || strings.Contains(w.Body.String(), "private-") {
				t.Fatalf("web read failed or exposed private configuration: %d", w.Code)
			}
		})
	}
	stored, err := store.GetMonitor(ctx, monitor.ID)
	if err != nil || stored.Target != monitor.Target || !strings.Contains(string(stored.Settings), "private-body") {
		t.Fatal("read projection changed saved configuration")
	}
}

func TestMonitorPrivateFormRoundTripAndRetargetGuard(t *testing.T) {
	h, store := storeHandler(t)
	ctx := context.Background()
	monitor := &storage.Monitor{Name: "public name", Type: "http", Target: "https://example.com/private-path?token=private-query", Interval: 60, Timeout: 10, Enabled: true, FailureThreshold: 1, SuccessThreshold: 1,
		Settings: json.RawMessage(`{"method":"POST","body":"private-body"}`)}
	if err := store.CreateMonitor(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	form := httpMonitorForm("", url.Values{"settings_auth_method": {"none"}, "settings_method": {"POST"}, "settings_body": {""}})
	update := func() *httptest.ResponseRecorder {
		r := adminRequest("POST", "/monitors/"+strconvI(monitor.ID), form)
		r.SetPathValue("id", strconvI(monitor.ID))
		key := &config.APIKeyConfig{Permissions: []string{"monitors.read", "monitors.update"}}
		r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, key))
		w := httptest.NewRecorder()
		h.MonitorUpdate(w, r)
		return w
	}
	if w := update(); w.Code != 303 {
		t.Fatalf("masked form save: %d %s", w.Code, w.Body)
	}
	stored, err := store.GetMonitor(ctx, monitor.ID)
	if err != nil || stored.Target != monitor.Target || !strings.Contains(string(stored.Settings), "private-body") {
		t.Fatal("form save lost hidden URL or body")
	}
	form.Set("target", "https://other.test")
	if w := update(); !strings.Contains(w.Body.String(), "credential destination") || strings.Contains(w.Body.String(), "private-") {
		t.Fatal("retarget did not show a safe replacement error")
	}
	stored, err = store.GetMonitor(ctx, monitor.ID)
	if err != nil || stored.Target != monitor.Target {
		t.Fatal("denied edit changed destination")
	}
	form.Set("clear_secrets", "body")
	if w := update(); w.Code != 303 {
		t.Fatalf("explicit payload clear: %d %s", w.Code, w.Body)
	}
	stored, err = store.GetMonitor(ctx, monitor.ID)
	if err != nil || stored.Target != "https://other.test" || strings.Contains(string(stored.Settings), "private-body") {
		t.Fatal("explicit replacement did not commit safely")
	}
}

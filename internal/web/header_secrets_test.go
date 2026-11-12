package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/storage"
)

func TestMonitorFormHidesCustomHeaderSecrets(t *testing.T) {
	for _, kind := range []string{"http", "websocket", "http_multi"} {
		t.Run(kind, func(t *testing.T) {
			h, store := storeHandler(t)
			settings := `{"headers":{"Arbitrary-Credential":"private-header"}}`
			if kind == "http_multi" {
				settings = `{"steps":[{"name":"step","url":"https://example.com","headers":{"Arbitrary-Credential":"private-header"}}]}`
			}
			monitor := &storage.Monitor{Name: kind, Type: kind, Target: "https://example.com", Interval: 60, Timeout: 10, Enabled: true, Settings: json.RawMessage(settings)}
			if err := store.CreateMonitor(context.Background(), monitor); err != nil {
				t.Fatal(err)
			}
			req := adminRequest("GET", "/monitors/"+strconv.FormatInt(monitor.ID, 10)+"/edit", nil)
			req.SetPathValue("id", strconv.FormatInt(monitor.ID, 10))
			w := httptest.NewRecorder()
			h.MonitorForm(w, req)
			if w.Code != 200 || strings.Contains(w.Body.String(), "private-header") {
				t.Fatalf("stored header exposed in %s form: status=%d", kind, w.Code)
			}
			stored, err := store.GetMonitor(context.Background(), monitor.ID)
			if err != nil || !strings.Contains(string(stored.Settings), "private-header") {
				t.Fatal("rendering modified stored credentials")
			}
		})
	}
}

func TestMonitorFormPreservesHiddenHeaders(t *testing.T) {
	h, store := storeHandler(t)
	ctx := context.Background()
	monitor := &storage.Monitor{Name: "API", Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10, Enabled: true, FailureThreshold: 1, SuccessThreshold: 1,
		Settings: json.RawMessage(`{"headers":{"Arbitrary-Credential":"private-header","Remove-This":"private-removed"}}`)}
	if err := store.CreateMonitor(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(monitor.ID, 10)
	form := httpMonitorForm("", url.Values{"settings_auth_method": {"none"}, "settings_header_key[]": {"Arbitrary-Credential"}, "settings_header_value[]": {""}})
	ordinary := false
	request := func() *httptest.ResponseRecorder {
		r := adminRequest("POST", "/monitors/"+id, form)
		if ordinary {
			key := &config.APIKeyConfig{Name: "editor", Permissions: []string{"monitors.read", "monitors.update"}}
			r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, key))
		}
		r.SetPathValue("id", id)
		w := httptest.NewRecorder()
		h.MonitorUpdate(w, r)
		return w
	}
	if w := request(); w.Code != 303 {
		t.Fatalf("update status = %d", w.Code)
	}
	stored, err := store.GetMonitor(ctx, monitor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored.Settings), "private-header") || strings.Contains(string(stored.Settings), "private-removed") {
		t.Fatalf("hidden header save/remove = %s", stored.Settings)
	}
	ordinary = true
	form.Set("target", "https://different.example.com")
	if w := request(); !strings.Contains(w.Body.String(), "credential destination") || strings.Contains(w.Body.String(), "private-header") {
		t.Fatal("ordinary retarget did not show a safe replacement error")
	}
	stored, err = store.GetMonitor(ctx, monitor.ID)
	if err != nil || stored.Target != monitor.Target {
		t.Fatal("denied retarget changed monitor")
	}
	form.Set("settings_header_value[]", "replacement")
	if w := request(); w.Code != 303 {
		t.Fatalf("replacement update status = %d", w.Code)
	}
	stored, err = store.GetMonitor(ctx, monitor.ID)
	if err != nil || stored.Target != "https://different.example.com" || !strings.Contains(string(stored.Settings), "replacement") {
		t.Fatal("explicit replacement did not permit new destination")
	}
	form.Set("settings_header_value[]", "")
	form.Set("target", "")
	if w := request(); strings.Contains(w.Body.String(), "private-header") {
		t.Fatal("validation error exposed a kept header")
	}
}

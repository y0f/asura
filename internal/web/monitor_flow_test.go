package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
)

func TestMonitorPrivateSourceFlowFormDenialIsAtomic(t *testing.T) {
	h, store := storeHandler(t)
	ctx := context.Background()
	mon := &storage.Monitor{Name: "login", Type: "http_multi", Target: "https://source.test", Enabled: true, Interval: 60, Timeout: 10, Settings: json.RawMessage(`{"steps":[{"name":"login","url":"https://source.test/login","method":"POST","body":"private-login-credential","extract_var":"token","extract_json":"access_token"}]}`)}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"name": {"changed metadata"}, "type": {"http_multi"}, "target": {mon.Target}, "interval": {"60"}, "timeout": {"10"}, "settings_mode": {"json"},
		"settings_json": {`{"steps":[{"name":"login","url":"https://source.test/login","method":"POST","body":"","extract_var":"token","extract_json":"access_token"},{"name":"sink","url":"https://new.test/receive","method":"POST","body":"{{token}}"}]}`}}
	r := adminRequest("POST", "/monitors/"+strconvI(mon.ID), form)
	r.SetPathValue("id", strconvI(mon.ID))
	r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, &config.APIKeyConfig{Permissions: []string{"monitors.read", "monitors.update"}}))
	w := httptest.NewRecorder()
	h.MonitorUpdate(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "changing a multi-step request chain") || strings.Contains(w.Body.String(), "private-login-credential") {
		t.Fatalf("borrowed credential form accepted/disclosed: %d", w.Code)
	}
	stored, err := store.GetMonitor(ctx, mon.ID)
	if err != nil || stored.Name != mon.Name || string(stored.Settings) != string(mon.Settings) {
		t.Fatalf("rejected form partially changed saved monitor: %v", err)
	}
}

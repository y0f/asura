package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
)

func TestProxyRoutingWebDeleteReturnsConflict(t *testing.T) {
	h, store := storeHandler(t)
	ctx := context.Background()
	p := &storage.Proxy{Name: "egress", Protocol: "http", Host: "proxy.test", Port: 8080, Enabled: true}
	if err := store.CreateProxy(ctx, p); err != nil {
		t.Fatal(err)
	}
	m := &storage.Monitor{Name: "routed", Type: "http", Target: "https://example.test", Interval: 60, Timeout: 10, ProxyID: &p.ID}
	if err := store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	r := adminRequest("POST", "/proxies/"+strconvI(p.ID)+"/delete", nil)
	r.SetPathValue("id", strconvI(p.ID))
	w := httptest.NewRecorder()
	h.ProxyDelete(w, r)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "change their routing") {
		t.Fatalf("referenced proxy deletion: %d %s", w.Code, w.Body.String())
	}
}

func TestProxyFormRetargetAndWriteOnlyRedirect(t *testing.T) {
	h, store := storeHandler(t)
	h.cfg.Server.BasePath = "/console"
	p := &storage.Proxy{Name: "private-proxy", Protocol: "http", Host: "proxy.test", Port: 8080, AuthUser: "user", AuthPass: "saved-secret", Enabled: true}
	if err := store.CreateProxy(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	key := &config.APIKeyConfig{Name: "writer", Permissions: []string{"proxies.update", "incidents.read"}}
	update := func(host, password, clear string) *httptest.ResponseRecorder {
		t.Helper()
		form := url.Values{"name": {"renamed"}, "protocol": {"http"}, "host": {host}, "port": {"8080"}, "auth_user": {"user"}, "auth_pass": {password}, "enabled": {"on"}, "clear_auth_pass": {clear}}
		r := adminRequest("POST", "/console/proxies/"+strconvI(p.ID), form)
		r.SetPathValue("id", strconvI(p.ID))
		r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, key))
		w := httptest.NewRecorder()
		h.ProxyUpdate(w, r)
		return w
	}
	w := update("other.test", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "credential destination") || strings.Contains(w.Body.String(), "saved-secret") || strings.Contains(w.Body.String(), "Saved. Leave blank") {
		t.Fatalf("retarget form leaked stored state or accepted credentials: %d %s", w.Code, w.Body.String())
	}
	got, err := store.GetProxy(context.Background(), p.ID)
	if err != nil || got.Host != p.Host || got.AuthPass != p.AuthPass {
		t.Fatal("rejected form changed the proxy")
	}
	w = update("proxy.test", "", "")
	if w.Code != 303 || w.Header().Get("Location") != "/console/incidents" {
		t.Fatalf("write-only redirect: %d %v", w.Code, w.Header())
	}
	got, err = store.GetProxy(context.Background(), p.ID)
	if err != nil || got.Name != "renamed" || got.AuthPass != p.AuthPass {
		t.Fatal("metadata edit dropped the password")
	}
	w = update("other.test", "", "on")
	got, err = store.GetProxy(context.Background(), p.ID)
	if w.Code != 303 || err != nil || got.Host != "other.test" || got.AuthPass != "" {
		t.Fatal("explicit clearing did not permit destination change")
	}
	if err := store.DeleteProxy(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	if w := update("other.test", "", ""); w.Code != 404 {
		t.Fatalf("missing proxy update: %d", w.Code)
	}
}

func TestProxyReadFailuresReturnErrors(t *testing.T) {
	h, store := storeHandler(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/proxies", h.Proxies}, {"/proxies/1/edit", h.ProxyForm},
	} {
		r := adminRequest("GET", tc.path, nil)
		r.SetPathValue("id", strconv.Itoa(1))
		w := httptest.NewRecorder()
		tc.handler(w, r)
		if w.Code != 500 {
			t.Fatalf("%s silently returned missing data: %d", tc.path, w.Code)
		}
	}
}

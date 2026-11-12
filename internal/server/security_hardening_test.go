package server

import (
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// A cross-site page must not be able to sign a browser in to a key of the
// attacker's choosing (login CSRF), through either login step.
func TestLoginFormsRequireSameOrigin(t *testing.T) {
	srv, _ := regressionServer(t, nil)
	t.Cleanup(srv.Close)
	post := func(path string, form url.Values, origin, referer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w
	}
	login := url.Values{"api_key": {regressionAdminKey}}
	for _, tc := range []struct{ origin, referer string }{
		{"https://attacker.example", ""},
		{"", "https://attacker.example/page"},
		{"", ""},
		{"null", ""},
	} {
		if w := post("/login", login, tc.origin, tc.referer); w.Code != http.StatusForbidden || len(w.Result().Cookies()) != 0 {
			t.Errorf("login with origin=%q referer=%q: %d, cookies=%d", tc.origin, tc.referer, w.Code, len(w.Result().Cookies()))
		}
		if w := post("/login/totp", url.Values{"challenge": {"x"}, "code": {"000000"}}, tc.origin, tc.referer); w.Code != http.StatusForbidden {
			t.Errorf("totp login with origin=%q referer=%q: %d", tc.origin, tc.referer, w.Code)
		}
	}
	if w := post("/login", login, "http://example.com", ""); w.Code != http.StatusSeeOther || len(w.Result().Cookies()) != 1 {
		t.Fatalf("same-origin login failed: %d", w.Code)
	}
	if w := post("/login", login, "", "http://example.com/login"); w.Code != http.StatusSeeOther {
		t.Fatalf("same-origin login with only a Referer failed: %d", w.Code)
	}
}

// HSTS is sent for HTTPS clients, including through a trusted TLS-terminating
// proxy, and never on the word of an untrusted client.
func TestHSTSBehindTrustedProxy(t *testing.T) {
	_, proxyNet, _ := net.ParseCIDR("10.0.0.0/8")
	handler := secureHeaders(nil, []net.IPNet{*proxyNet})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, tc := range []struct {
		name, remote, proto string
		tls, want           bool
	}{
		{"direct TLS", "203.0.113.1:1", "", true, true},
		{"trusted proxy https", "10.1.2.3:1", "https", false, true},
		{"trusted proxy http", "10.1.2.3:1", "http", false, false},
		{"untrusted forwarded https", "203.0.113.1:1", "https", false, false},
		{"plain http", "203.0.113.1:1", "", false, false},
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = tc.remote
		if tc.proto != "" {
			req.Header.Set("X-Forwarded-Proto", tc.proto)
		}
		if tc.tls {
			req.TLS = &tls.ConnectionState{}
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if got := w.Header().Get("Strict-Transport-Security") != ""; got != tc.want {
			t.Errorf("%s: HSTS sent=%v, want %v", tc.name, got, tc.want)
		}
	}
}

// One credential cannot occupy every live-update stream.
func TestSSEStreamsAreCappedPerKey(t *testing.T) {
	broker := NewEventBroker(slog.New(slog.NewTextHandler(io.Discard, nil)))
	var held []*sseClient
	for i := 0; i < maxSSEClientsPerOwner; i++ {
		c := broker.SubscribeOwner("viewer")
		if c == nil {
			t.Fatalf("stream %d refused below the per-key cap", i)
		}
		held = append(held, c)
	}
	if broker.SubscribeOwner("viewer") != nil {
		t.Fatal("a key exceeded its stream cap")
	}
	if broker.SubscribeOwner("admin") == nil {
		t.Fatal("another key was locked out by one key's streams")
	}
	broker.Unsubscribe(held[0])
	if broker.SubscribeOwner("viewer") == nil {
		t.Fatal("closing a stream did not free its slot")
	}
}

package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/y0f/asura/v3/internal/model"
)

type proxyProbe struct {
	called bool
	kind   string
}

func (c *proxyProbe) Type() string { return c.kind }
func (c *proxyProbe) Check(context.Context, *model.Monitor) (*Result, error) {
	c.called = true
	return &Result{Status: "up"}, nil
}

func TestProxyAdmissionNeverFallsBackToDirect(t *testing.T) {
	id := int64(1)
	for _, tc := range []struct {
		name, kind, endpoint, settings string
		proxyID                        *int64
		allowed                        bool
	}{
		{"direct", "http", "", "", nil, true},
		{"disabled or missing", "http", "", "", &id, false},
		{"invalid port", "http", "http://user:secret@proxy.test:99999", "", &id, false},
		{"unsupported scheme", "http", "ftp://user:secret@proxy.test:80", "", &id, false},
		{"malformed", "http", "http://user:secret@proxy.test/bad", "", &id, false},
		{"http tcp", "tcp", "http://proxy.test:8080", "", &id, false},
		{"socks unsupported", "smtp", "socks5://proxy.test:1080", "", &id, false},
		{"dns default resolver", "dns", "socks5://proxy.test:1080", `{}`, &id, false},
		{"dns explicit resolver", "dns", "socks5://proxy.test:1080", `{"server":"192.0.2.53"}`, &id, true},
		{"http supported", "http", "http://proxy.test:8080", "", &id, true},
		{"socks tcp", "tcp", "socks5://proxy.test:1080", "", &id, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &proxyProbe{kind: tc.kind}
			r := NewRegistry()
			r.Register(probe)
			c, err := r.Get(tc.kind)
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Check(context.Background(), &model.Monitor{Type: tc.kind, ProxyID: tc.proxyID, ProxyURL: tc.endpoint, Settings: json.RawMessage(tc.settings)})
			if probe.called != tc.allowed || (err == nil) != tc.allowed {
				t.Fatalf("called=%v err=%v", probe.called, err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("error exposed credential")
			}
		})
	}
}

func TestProxyOAuthTokenUsesRouteAndConfiguration(t *testing.T) {
	requests := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Host != "token.invalid" || r.Method != "POST" {
			t.Errorf("unexpected route: %s %s", r.Method, r.URL.Host)
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		fmt.Fprintf(w, `{"access_token":%q,"expires_in":3600}`, r.FormValue("client_secret"))
	}))
	defer proxy.Close()
	m := &model.Monitor{ID: 7001, Type: "http", Target: "https://target.invalid", ProxyURL: proxy.URL}
	defer ClearOAuth2TokenCache(m.ID)
	s := model.HTTPSettings{OAuth2TokenURL: "http://token.invalid/token", OAuth2ClientID: "client", OAuth2ClientSecret: "first"}
	for _, secret := range []string{"first", "first", "replacement"} {
		s.OAuth2ClientSecret = secret
		token, err := fetchOAuth2Token(context.Background(), m, s, true)
		if err != nil || token != secret {
			t.Fatalf("token changed or direct route used: %v", err)
		}
	}
	if requests != 2 {
		t.Fatalf("cache did not preserve identical config and invalidate changed credentials: %d", requests)
	}
	// A route change must not reuse a token fetched through the previous proxy.
	m.ProxyURL = "http://127.0.0.1:0"
	if _, err := fetchOAuth2Token(context.Background(), m, s, true); err == nil {
		t.Fatal("route change reused stale token")
	}
}

func TestProxyOAuthTokenRejectsRedirectAndSecretErrors(t *testing.T) {
	forwarded := false
	recipient := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true }))
	defer recipient.Close()
	for _, status := range []int{307, 401} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", recipient.URL)
				w.WriteHeader(status)
				fmt.Fprint(w, "echoed-client-secret")
			}))
			defer server.Close()
			m := &model.Monitor{ID: int64(status)}
			defer ClearOAuth2TokenCache(m.ID)
			_, err := fetchOAuth2Token(context.Background(), m, model.HTTPSettings{OAuth2TokenURL: server.URL, OAuth2ClientSecret: "echoed-client-secret"}, true)
			if err == nil || forwarded || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe OAuth response: %v", err)
			}
		})
	}
}

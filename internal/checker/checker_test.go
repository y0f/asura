package checker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/storage"
)

func TestHTTPChecker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	checker := &HTTPChecker{AllowPrivate: true}
	monitor := &storage.Monitor{
		Target:  server.URL,
		Timeout: 5,
	}

	result, err := checker.Check(context.Background(), monitor)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "up" {
		t.Fatalf("expected up, got %s: %s", result.Status, result.Message)
	}
	if result.StatusCode != 200 {
		t.Fatalf("expected status 200, got %d", result.StatusCode)
	}
	if result.Body != `{"status":"ok"}` {
		t.Fatalf("unexpected body: %s", result.Body)
	}
	if result.BodyHash == "" {
		t.Fatal("expected body hash")
	}
}

func TestHTTPCheckerWithSettings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("X-Custom") != "test" {
			t.Fatal("expected custom header")
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	settings, _ := json.Marshal(storage.HTTPSettings{
		Method:  "POST",
		Headers: map[string]string{"X-Custom": "test"},
	})

	checker := &HTTPChecker{AllowPrivate: true}
	monitor := &storage.Monitor{
		Target:   server.URL,
		Timeout:  5,
		Settings: settings,
	}

	result, err := checker.Check(context.Background(), monitor)
	if err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != 201 {
		t.Fatalf("expected 201, got %d", result.StatusCode)
	}
}

func TestHTTPCheckerDown(t *testing.T) {
	checker := &HTTPChecker{AllowPrivate: true}
	monitor := &storage.Monitor{
		Target:  "http://192.0.2.1:1", // non-routable, will timeout
		Timeout: 1,
	}

	result, err := checker.Check(context.Background(), monitor)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "down" {
		t.Fatalf("expected down, got %s", result.Status)
	}
}

func TestHTTPCheckerExpectedStatus(t *testing.T) {
	tests := []struct {
		name           string
		serverStatus   int
		expectedStatus int
		wantStatus     string
		wantMessage    string
	}{
		{
			name:           "matching expected status",
			serverStatus:   200,
			expectedStatus: 200,
			wantStatus:     "up",
		},
		{
			name:           "mismatched expected status",
			serverStatus:   502,
			expectedStatus: 200,
			wantStatus:     "down",
			wantMessage:    "expected status 200, got 502",
		},
		{
			name:           "no expected status set",
			serverStatus:   502,
			expectedStatus: 0,
			wantStatus:     "up",
		},
		{
			name:           "expected non-200 status matches",
			serverStatus:   201,
			expectedStatus: 201,
			wantStatus:     "up",
		},
		{
			name:           "expected non-200 status mismatches",
			serverStatus:   200,
			expectedStatus: 201,
			wantStatus:     "down",
			wantMessage:    "expected status 201, got 200",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.serverStatus)
			}))
			defer server.Close()

			settings, _ := json.Marshal(storage.HTTPSettings{
				ExpectedStatus: tt.expectedStatus,
			})

			c := &HTTPChecker{AllowPrivate: true}
			monitor := &storage.Monitor{
				Target:   server.URL,
				Timeout:  5,
				Settings: settings,
			}

			result, err := c.Check(context.Background(), monitor)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q (message: %s)", result.Status, tt.wantStatus, result.Message)
			}
			if tt.wantMessage != "" && result.Message != tt.wantMessage {
				t.Errorf("message = %q, want %q", result.Message, tt.wantMessage)
			}
			if result.StatusCode != tt.serverStatus {
				t.Errorf("status_code = %d, want %d", result.StatusCode, tt.serverStatus)
			}
		})
	}
}

func TestRegistryGetUnregistered(t *testing.T) {
	r := NewRegistry()
	_, err := r.Get("unknown")
	if err == nil {
		t.Fatal("expected error for unregistered type")
	}
}

func TestDefaultRegistryHasAllTypes(t *testing.T) {
	r := DefaultRegistry(nil, false)
	types := []string{"http", "tcp", "dns", "icmp", "tls", "websocket", "command", "docker"}
	for _, typ := range types {
		if _, err := r.Get(typ); err != nil {
			t.Fatalf("expected %s checker, got error: %v", typ, err)
		}
	}
}

func TestResolveMaxRedirects(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }

	tests := []struct {
		name string
		s    storage.HTTPSettings
		want int
	}{
		{"max_redirects set", storage.HTTPSettings{MaxRedirects: 5}, 5},
		{"max_redirects zero with follow false", storage.HTTPSettings{MaxRedirects: 0, FollowRedirects: boolPtr(false)}, 0},
		{"follow true", storage.HTTPSettings{FollowRedirects: boolPtr(true)}, 10},
		{"follow false", storage.HTTPSettings{FollowRedirects: boolPtr(false)}, 0},
		{"both unset defaults to 10", storage.HTTPSettings{}, 10},
		{"max_redirects takes priority over follow", storage.HTTPSettings{MaxRedirects: 3, FollowRedirects: boolPtr(false)}, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveMaxRedirects(tt.s)
			if got != tt.want {
				t.Errorf("resolveMaxRedirects() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestHTTPCheckerUpsideDownIgnored(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := &HTTPChecker{AllowPrivate: true}

	t.Run("checker returns raw status", func(t *testing.T) {
		mon := &storage.Monitor{Target: server.URL, Timeout: 5, UpsideDown: true}
		result, err := c.Check(context.Background(), mon)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "up" {
			t.Errorf("status = %q, want up (UpsideDown handled by pipeline, not checker)", result.Status)
		}
	})

	t.Run("expected status mismatch returns down", func(t *testing.T) {
		settings, _ := json.Marshal(storage.HTTPSettings{ExpectedStatus: 404})
		mon := &storage.Monitor{Target: server.URL, Timeout: 5, UpsideDown: true, Settings: settings}
		result, err := c.Check(context.Background(), mon)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "down" {
			t.Errorf("status = %q, want down (UpsideDown handled by pipeline, not checker)", result.Status)
		}
	})
}

func TestHTTPCheckerCacheBuster(t *testing.T) {
	var receivedURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		w.WriteHeader(200)
	}))
	defer server.Close()

	settings, _ := json.Marshal(storage.HTTPSettings{CacheBuster: true})
	c := &HTTPChecker{AllowPrivate: true}
	mon := &storage.Monitor{Target: server.URL + "/test", Timeout: 5, Settings: settings}

	_, err := c.Check(context.Background(), mon)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(receivedURL, "_=") {
		t.Errorf("URL should contain cache buster param, got %q", receivedURL)
	}
}

func TestHTTPCheckerCacheBusterWithExistingQuery(t *testing.T) {
	var receivedURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		w.WriteHeader(200)
	}))
	defer server.Close()

	settings, _ := json.Marshal(storage.HTTPSettings{CacheBuster: true})
	c := &HTTPChecker{AllowPrivate: true}
	mon := &storage.Monitor{Target: server.URL + "/test?foo=bar", Timeout: 5, Settings: settings}

	_, err := c.Check(context.Background(), mon)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(receivedURL, "&_=") {
		t.Errorf("URL should use & for cache buster with existing query, got %q", receivedURL)
	}
}

func TestHTTPCheckerBodyEncoding(t *testing.T) {
	tests := []struct {
		name     string
		encoding string
		wantCT   string
	}{
		{"json", "json", "application/json"},
		{"xml", "xml", "application/xml"},
		{"form", "form", "application/x-www-form-urlencoded"},
		{"raw", "raw", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotCT string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotCT = r.Header.Get("Content-Type")
				w.WriteHeader(200)
			}))
			defer server.Close()

			settings, _ := json.Marshal(storage.HTTPSettings{
				Method:       "POST",
				Body:         "test",
				BodyEncoding: tt.encoding,
			})

			c := &HTTPChecker{AllowPrivate: true}
			mon := &storage.Monitor{Target: server.URL, Timeout: 5, Settings: settings}
			c.Check(context.Background(), mon)

			if tt.wantCT != "" && gotCT != tt.wantCT {
				t.Errorf("Content-Type = %q, want %q", gotCT, tt.wantCT)
			}
			if tt.wantCT == "" && gotCT != "" {
				t.Errorf("Content-Type should be empty for raw, got %q", gotCT)
			}
		})
	}
}

func TestHTTPCheckerAuthMethods(t *testing.T) {
	t.Run("basic auth", func(t *testing.T) {
		var gotAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(200)
		}))
		defer server.Close()

		settings, _ := json.Marshal(storage.HTTPSettings{
			AuthMethod:    "basic",
			BasicAuthUser: "admin",
			BasicAuthPass: "secret",
		})

		c := &HTTPChecker{AllowPrivate: true}
		mon := &storage.Monitor{Target: server.URL, Timeout: 5, Settings: settings}
		c.Check(context.Background(), mon)

		if !strings.HasPrefix(gotAuth, "Basic ") {
			t.Errorf("expected Basic auth header, got %q", gotAuth)
		}
	})

	t.Run("bearer auth", func(t *testing.T) {
		var gotAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(200)
		}))
		defer server.Close()

		settings, _ := json.Marshal(storage.HTTPSettings{
			AuthMethod:  "bearer",
			BearerToken: "my-token",
		})

		c := &HTTPChecker{AllowPrivate: true}
		mon := &storage.Monitor{Target: server.URL, Timeout: 5, Settings: settings}
		c.Check(context.Background(), mon)

		if gotAuth != "Bearer my-token" {
			t.Errorf("expected 'Bearer my-token', got %q", gotAuth)
		}
	})

	t.Run("none auth", func(t *testing.T) {
		var gotAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(200)
		}))
		defer server.Close()

		settings, _ := json.Marshal(storage.HTTPSettings{AuthMethod: "none"})

		c := &HTTPChecker{AllowPrivate: true}
		mon := &storage.Monitor{Target: server.URL, Timeout: 5, Settings: settings}
		c.Check(context.Background(), mon)

		if gotAuth != "" {
			t.Errorf("expected no auth header, got %q", gotAuth)
		}
	})

	t.Run("legacy basic auth without auth_method", func(t *testing.T) {
		var gotAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(200)
		}))
		defer server.Close()

		settings, _ := json.Marshal(storage.HTTPSettings{
			BasicAuthUser: "user",
			BasicAuthPass: "pass",
		})

		c := &HTTPChecker{AllowPrivate: true}
		mon := &storage.Monitor{Target: server.URL, Timeout: 5, Settings: settings}
		c.Check(context.Background(), mon)

		if !strings.HasPrefix(gotAuth, "Basic ") {
			t.Errorf("legacy basic auth should work without auth_method, got %q", gotAuth)
		}
	})
}

func TestHTTPCheckerOAuth2(t *testing.T) {
	t.Run("successful token fetch", func(t *testing.T) {
		var gotAuth string
		// target server that validates bearer token
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(200)
		}))
		defer target.Close()

		// token server
		tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				t.Errorf("expected POST to token endpoint, got %s", r.Method)
			}
			if r.FormValue("grant_type") != "client_credentials" {
				t.Errorf("expected grant_type=client_credentials, got %q", r.FormValue("grant_type"))
			}
			if r.FormValue("client_id") != "my-client" {
				t.Errorf("expected client_id=my-client, got %q", r.FormValue("client_id"))
			}
			if r.FormValue("scope") != "read write" {
				t.Errorf("expected scope='read write', got %q", r.FormValue("scope"))
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"test-token-123","expires_in":3600}`))
		}))
		defer tokenServer.Close()

		settings, _ := json.Marshal(storage.HTTPSettings{
			AuthMethod:         "oauth2",
			OAuth2TokenURL:     tokenServer.URL,
			OAuth2ClientID:     "my-client",
			OAuth2ClientSecret: "my-secret",
			OAuth2Scopes:       "read write",
		})

		c := &HTTPChecker{AllowPrivate: true}
		mon := &storage.Monitor{ID: 9990, Target: target.URL, Timeout: 5, Settings: settings}
		defer ClearOAuth2TokenCache(9990)

		result, err := c.Check(context.Background(), mon)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "up" {
			t.Fatalf("expected up, got %s: %s", result.Status, result.Message)
		}
		if gotAuth != "Bearer test-token-123" {
			t.Errorf("expected 'Bearer test-token-123', got %q", gotAuth)
		}
	})

	t.Run("token cached across checks", func(t *testing.T) {
		tokenFetches := 0
		tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenFetches++
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"cached-token","expires_in":3600}`))
		}))
		defer tokenServer.Close()

		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		}))
		defer target.Close()

		settings, _ := json.Marshal(storage.HTTPSettings{
			AuthMethod:         "oauth2",
			OAuth2TokenURL:     tokenServer.URL,
			OAuth2ClientID:     "client",
			OAuth2ClientSecret: "secret",
		})

		c := &HTTPChecker{AllowPrivate: true}
		mon := &storage.Monitor{ID: 9991, Target: target.URL, Timeout: 5, Settings: settings}
		defer ClearOAuth2TokenCache(9991)

		c.Check(context.Background(), mon)
		c.Check(context.Background(), mon)
		c.Check(context.Background(), mon)

		if tokenFetches != 1 {
			t.Errorf("expected 1 token fetch (cached), got %d", tokenFetches)
		}
	})

	t.Run("token fetch failure", func(t *testing.T) {
		tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"invalid_client"}`))
		}))
		defer tokenServer.Close()

		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		}))
		defer target.Close()

		settings, _ := json.Marshal(storage.HTTPSettings{
			AuthMethod:         "oauth2",
			OAuth2TokenURL:     tokenServer.URL,
			OAuth2ClientID:     "bad-client",
			OAuth2ClientSecret: "bad-secret",
		})

		c := &HTTPChecker{AllowPrivate: true}
		mon := &storage.Monitor{ID: 9992, Target: target.URL, Timeout: 5, Settings: settings}
		defer ClearOAuth2TokenCache(9992)

		result, err := c.Check(context.Background(), mon)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "down" {
			t.Fatalf("expected down, got %s", result.Status)
		}
		if !strings.Contains(result.Message, "oauth2 token fetch failed") {
			t.Errorf("expected oauth2 error message, got %q", result.Message)
		}
	})
}

func TestHTTPCheckerMTLS(t *testing.T) {
	t.Run("invalid cert/key pair", func(t *testing.T) {
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		}))
		defer target.Close()

		settings, _ := json.Marshal(storage.HTTPSettings{
			MTLSEnabled:    true,
			MTLSClientCert: "not-a-cert",
			MTLSClientKey:  "not-a-key",
		})

		c := &HTTPChecker{AllowPrivate: true}
		mon := &storage.Monitor{Target: target.URL, Timeout: 5, Settings: settings}

		result, err := c.Check(context.Background(), mon)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "down" {
			t.Fatalf("expected down, got %s", result.Status)
		}
		if !strings.Contains(result.Message, "mtls config failed") {
			t.Errorf("expected mtls error message, got %q", result.Message)
		}
	})
}

func TestHTTPCheckerNoRedirects(t *testing.T) {
	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/target", http.StatusFound)
			return
		}
		w.WriteHeader(200)
	}))
	defer redirectServer.Close()

	f := false
	settings, _ := json.Marshal(storage.HTTPSettings{FollowRedirects: &f})

	c := &HTTPChecker{AllowPrivate: true}
	mon := &storage.Monitor{Target: redirectServer.URL + "/redirect", Timeout: 5, Settings: settings}

	result, err := c.Check(context.Background(), mon)
	if err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != 302 {
		t.Errorf("should not follow redirect, got status %d", result.StatusCode)
	}
}

func TestHTTPCheckerRequestBody(t *testing.T) {
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.WriteHeader(200)
	}))
	defer server.Close()

	settings, _ := json.Marshal(storage.HTTPSettings{
		Method: "POST",
		Body:   `{"key":"value"}`,
	})

	c := &HTTPChecker{AllowPrivate: true}
	mon := &storage.Monitor{Target: server.URL, Timeout: 5, Settings: settings}
	c.Check(context.Background(), mon)

	if receivedBody != `{"key":"value"}` {
		t.Errorf("body = %q", receivedBody)
	}
}

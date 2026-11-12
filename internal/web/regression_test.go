package web

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/notifier"
	"github.com/y0f/asura/v3/internal/storage"
)

func createProtectedStatusPage(t *testing.T, store *storage.SQLiteStore, password bool) (*storage.StatusPage, *storage.Monitor) {
	t.Helper()
	ctx := context.Background()
	mon := &storage.Monitor{Name: "secret-billing-db", Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10,
		Enabled: true, FailureThreshold: 1, SuccessThreshold: 1}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}
	sp := &storage.StatusPage{Slug: "internal", Title: "Internal", Enabled: true, ShowIncidents: true}
	if password {
		sp.PasswordHash = "$2a$10$abcdefghijklmnopqrstuuJ0qY3m0b1i9X5x0aZr1oQ7m3sV4wW8e"
		sp.PasswordEnabled = true
	}
	if err := store.CreateStatusPage(ctx, sp); err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatusPageMonitors(ctx, sp.ID, []storage.StatusPageMonitor{{PageID: sp.ID, MonitorID: mon.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateIncident(ctx, &storage.Incident{MonitorID: mon.ID, MonitorName: mon.Name, Status: "open", Severity: "critical",
		Cause: "db down", StartedAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	return sp, mon
}

// Regression: password-protected status pages leak via /rss and /subscribe.
func TestPasswordStatusPageProtectsRSSAndSubscribe(t *testing.T) {
	h, store := storeHandler(t)
	h.cfg.Subscriptions.Enabled = true
	h.loginRL = httputil.NewRateLimiter(10, 10)
	sp, mon := createProtectedStatusPage(t, store, true)

	w := httptest.NewRecorder()
	h.StatusPageByID(w, httptest.NewRequest("GET", "/internal", nil), sp.ID)
	if w.Code != http.StatusFound {
		t.Fatalf("precondition: protected page should redirect to auth, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	h.StatusPageRSS(w, httptest.NewRequest("GET", "/internal/rss", nil), sp.ID)
	rssLeak := w.Code == http.StatusOK && strings.Contains(w.Body.String(), mon.Name)

	form := url.Values{"type": {"webhook"}, "webhook_url": {"https://attacker.example/hook"}}
	r := httptest.NewRequest("POST", "/internal/subscribe", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	h.StatusPageSubscribe(w, r, sp.ID)
	subs, _ := store.ListConfirmedSubscribers(context.Background(), sp.ID)

	if rssLeak || len(subs) > 0 {
		t.Fatalf("unauthenticated access to password-protected page: rss leaked incident for %q=%v, confirmed webhook subscribers created=%d",
			mon.Name, rssLeak, len(subs))
	}
}

// fakeSMTP accepts connections and speaks just enough SMTP to receive a message.
type fakeSMTP struct {
	l        net.Listener
	accepted atomic.Int64
	messages atomic.Int64
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{l: l}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			f.accepted.Add(1)
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	fmt.Fprint(c, "220 fake ESMTP\r\n")
	inData := false
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if inData {
			if line == ".\r\n" {
				inData = false
				f.messages.Add(1)
				fmt.Fprint(c, "250 queued\r\n")
			}
			continue
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			fmt.Fprint(c, "250 fake\r\n")
		case strings.HasPrefix(cmd, "DATA"):
			inData = true
			fmt.Fprint(c, "354 go\r\n")
		case strings.HasPrefix(cmd, "QUIT"):
			fmt.Fprint(c, "221 bye\r\n")
			return
		default:
			fmt.Fprint(c, "250 ok\r\n")
		}
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// Regression: confirmation email is sent with the (soon cancelled) request context.
func TestSubscribeConfirmationEmailOutlivesRequest(t *testing.T) {
	h, store := storeHandler(t)
	h.cfg.Subscriptions.Enabled = true
	h.loginRL = httputil.NewRateLimiter(100, 100)
	t.Cleanup(h.loginRL.Stop)
	var logs syncBuf
	h.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError}))
	sp, _ := createProtectedStatusPage(t, store, false)

	smtpSrv := startFakeSMTP(t)
	port := smtpSrv.l.Addr().(*net.TCPAddr).Port
	h.subNotifier = notifier.NewSubscriberNotifier(store, config.SMTPConfig{Host: "127.0.0.1", Port: port, From: "status@example.com", TLSMode: "none"},
		"http://status.example", h.logger, true)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		h.subNotifier.Shutdown(ctx)
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.StatusPageSubscribe(w, r, sp.ID) }))
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	const n = 10
	for i := 0; i < n; i++ {
		resp, err := client.PostForm(srv.URL, url.Values{"type": {"email"}, "email": {fmt.Sprintf("u%d@example.com", i)}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if !strings.Contains(resp.Header.Get("Location"), "Check+your+email") {
			t.Fatalf("precondition: expected confirmation redirect, got %q", resp.Header.Get("Location"))
		}
	}
	time.Sleep(1500 * time.Millisecond)
	got := smtpSrv.messages.Load()
	cancelled := strings.Count(logs.String(), "canceled")
	t.Logf("%d subscriptions: smtp accepted=%d messages=%d, cancelled-dial errors logged=%d", n, smtpSrv.accepted.Load(), got, cancelled)
	if got < n {
		t.Fatalf("only %d of %d confirmation emails were delivered; %d failed with a cancelled dial (request context)", got, n, cancelled)
	}
}

type failingSessionStore struct {
	*storage.SQLiteStore
}

func (s *failingSessionStore) GetSessionByTokenHash(context.Context, string) (*storage.Session, error) {
	return nil, errors.New("database is locked")
}

// Regression: transient session-lookup DB error clears the cookie.
func TestTransientSessionErrorKeepsCookie(t *testing.T) {
	h, store := storeHandler(t)
	h.store = &failingSessionStore{SQLiteStore: store}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "valid-token"})
	w := httptest.NewRecorder()
	h.RequireAuth(next).ServeHTTP(w, r)

	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie && c.MaxAge < 0 {
			t.Fatalf("transient DB error deleted the session cookie (status %d, Location %q)", w.Code, w.Header().Get("Location"))
		}
	}
}

func newLoginHandler(t *testing.T, cfg *config.Config) *Handler {
	t.Helper()
	h, _ := storeHandler(t)
	h.cfg = cfg
	h.loginRL = httputil.NewRateLimiter(cfg.Auth.Login.RateLimitPerSec, cfg.Auth.Login.RateLimitBurst)
	t.Cleanup(h.loginRL.Stop)
	return h
}

func postLogin(t *testing.T, h *Handler, rawKey string) (*httptest.ResponseRecorder, *http.Cookie) {
	t.Helper()
	form := url.Values{"api_key": {rawKey}}
	r := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	h.LoginPost(w, r)
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			return w, c
		}
	}
	return w, nil
}

func authedGet(h *Handler, c *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	w := httptest.NewRecorder()
	h.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })).ServeHTTP(w, r)
	return w
}

// Regression: configs that make web login impossible (duplicate key names log
// the second key out on every request, lifetime 0 loops back to /login,
// burst 0 refuses every login) must be rejected at validation time.
func TestLockoutAuthConfigsRejected(t *testing.T) {
	cases := map[string]func(*config.Config){
		"duplicate key names": func(c *config.Config) {
			c.Auth.APIKeys = append(c.Auth.APIKeys, config.APIKeyConfig{Name: "admin", Hash: config.HashAPIKey("other"), Role: "readonly"})
		},
		"session lifetime 0": func(c *config.Config) { c.Auth.Session.Lifetime = 0 },
		"login burst 0":      func(c *config.Config) { c.Auth.Login.RateLimitBurst = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "admin", Hash: config.HashAPIKey("k"), Role: "admin"}}
			mutate(cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("%s: config validated", name)
			}
		})
	}
}

// A validated default config keeps a fresh login usable on the next request.
func TestDefaultConfigLoginSurvivesNextRequest(t *testing.T) {
	cfg := config.Defaults()
	cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "admin", Hash: config.HashAPIKey("k"), Role: "admin"}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	h := newLoginHandler(t, cfg)
	lw, cookie := postLogin(t, h, "k")
	if cookie == nil {
		t.Fatalf("login failed: %d", lw.Code)
	}
	if w := authedGet(h, cookie); w.Code != 200 {
		t.Fatalf("next request after login -> %d %q", w.Code, w.Header().Get("Location"))
	}
}

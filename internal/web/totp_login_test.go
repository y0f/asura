package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/storage"
	"github.com/y0f/asura/v3/internal/totp"
)

const totpTestKey = "totp-test-key-0123456789abcdefghijklmnop"

var challengePattern = regexp.MustCompile(`name="challenge" value="([0-9a-f]{64})"`)

func totpHandler(t *testing.T) (*Handler, []byte) {
	t.Helper()
	f, err := os.CreateTemp("", "asura-totp-*.db")
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
	cfg := config.Defaults()
	cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "admin", Hash: config.HashAPIKey(totpTestKey), SuperAdmin: true, TOTP: true}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	secret, err := totp.GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTOTPKey(context.Background(), &storage.TOTPKey{APIKeyName: "admin", Secret: totp.EncodeSecret(secret)}); err != nil {
		t.Fatal(err)
	}
	// A generous per-IP budget isolates the per-key limit under test.
	loginRL := httputil.NewRateLimiter(1000, 1000)
	t.Cleanup(loginRL.Stop)
	h := New(cfg, store, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), "test", "frame-ancestors 'none'", loginRL)
	t.Cleanup(h.Stop)
	return h, secret
}

func totpChallengeFor(t *testing.T, h *Handler, ip string) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{"api_key": {totpTestKey}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://example.com")
	r.RemoteAddr = ip + ":1"
	w := httptest.NewRecorder()
	h.LoginPost(w, r)
	m := challengePattern.FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatalf("no TOTP challenge issued: %d %s", w.Code, w.Body)
	}
	return m[1]
}

func postTOTP(h *Handler, ip, challenge, code string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/login/totp", strings.NewReader(url.Values{"challenge": {challenge}, "code": {code}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://example.com")
	r.RemoteAddr = ip + ":1"
	w := httptest.NewRecorder()
	h.TOTPLoginPost(w, r)
	return w
}

func sessionIssued(w *httptest.ResponseRecorder) bool {
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			return true
		}
	}
	return false
}

func wrongCode(secret []byte) string {
	now := time.Now()
	for _, candidate := range []string{"000000", "111111", "222222"} {
		if !totp.Validate(secret, candidate, now) {
			return candidate
		}
	}
	return "333333"
}

func TestTOTPLoginSucceedsOnceAndRejectsReplay(t *testing.T) {
	h, secret := totpHandler(t)
	code := totp.Code(secret, time.Now())
	if w := postTOTP(h, "198.51.100.1", totpChallengeFor(t, h, "198.51.100.1"), code); w.Code != http.StatusSeeOther || !sessionIssued(w) {
		t.Fatalf("valid TOTP code did not sign in: %d", w.Code)
	}
	if w := postTOTP(h, "198.51.100.1", totpChallengeFor(t, h, "198.51.100.1"), code); sessionIssued(w) {
		t.Fatal("a used TOTP code signed in again")
	}
}

// Guesses for one key are limited even when every guess comes from a new
// address, and a limited key does not test further codes, even correct ones.
func TestTOTPFailuresAreLimitedPerKey(t *testing.T) {
	h, secret := totpHandler(t)
	wrong := wrongCode(secret)
	for i := 0; i < totpKeyFailureBurst; i++ {
		ip := "203.0.113." + string(rune('1'+i%9))
		if w := postTOTP(h, ip, totpChallengeFor(t, h, ip), wrong); sessionIssued(w) || !strings.Contains(w.Body.String(), "Invalid code") {
			t.Fatalf("failure %d was not reported as an invalid code: %s", i, w.Body)
		}
	}
	w := postTOTP(h, "192.0.2.77", totpChallengeFor(t, h, "192.0.2.77"), totp.Code(secret, time.Now()))
	if sessionIssued(w) || !strings.Contains(w.Body.String(), "Too many login attempts") {
		t.Fatalf("key was not limited after %d failures from different addresses: %d", totpKeyFailureBurst, w.Code)
	}
}

package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/v2/internal/config"
	"github.com/y0f/asura/v2/internal/pageauth"
)

func TestStatusPageCookieAuthorityAcrossPublicRoutes(t *testing.T) {
	for _, base := range []string{"", "/asura"} {
		t.Run(base, func(t *testing.T) {
			srv, _ := regressionServer(t, func(cfg *config.Config) {
				cfg.Server.BasePath = base
				cfg.Auth.Session.CookieSecure = true
				cfg.Subscriptions.Enabled = true
				cfg.Monitor.AllowPrivateTargets = true
			})
			t.Cleanup(srv.Close)
			page := createTestStatusPage(t, srv, "page-secret")
			page.Title = "private-page-title"
			if err := srv.store.UpdateStatusPage(context.Background(), page); err != nil {
				t.Fatal(err)
			}
			form := url.Values{"password": {"page-secret"}}
			login := httptest.NewRequest(http.MethodPost, base+"/"+page.Slug+"/auth", strings.NewReader(form.Encode()))
			login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, login)
			if w.Code != http.StatusSeeOther || len(w.Result().Cookies()) != 1 {
				t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
			}
			cookie := w.Result().Cookies()[0]
			// The helper stores a legacy unsalted hash; a verified login
			// replaces it with a salted PBKDF2 hash of the same password.
			legacyHash := page.PasswordHash
			if page, err := srv.store.GetStatusPage(context.Background(), page.ID); err != nil {
				t.Fatal(err)
			} else if page.PasswordHash == legacyHash || pageauth.NeedsRehash(page.PasswordHash) || !pageauth.Verify(page.PasswordHash, "page-secret") {
				t.Fatalf("legacy page password hash was not upgraded on login: %q", page.PasswordHash)
			}
			reloaded, err := srv.store.GetStatusPage(context.Background(), page.ID)
			if err != nil {
				t.Fatal(err)
			}
			page = reloaded
			if cookie.Path != base+"/" || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != int(pageauth.CookieLifetime/time.Second) {
				t.Fatalf("cookie scope or browser protections changed: %+v", cookie)
			}
			if !pageauth.VerifyCookie(cookie.Value, page.ID, page.PasswordHash, time.Now()) {
				t.Error("login did not issue a server-expiring page-bound cookie")
			}
			expired, err := pageauth.IssueCookie(page.ID, page.PasswordHash, time.Now().Add(-pageauth.CookieLifetime-time.Second))
			if err != nil {
				t.Fatal(err)
			}
			otherPage, err := pageauth.IssueCookie(page.ID+1, page.PasswordHash, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			legacy := fmt.Sprintf("%x", sha256.Sum256([]byte(page.PasswordHash+":sp-auth")))
			for _, credential := range []struct {
				name, value string
				valid       bool
			}{{"current", cookie.Value, true}, {"expired", expired, false}, {"other page", otherPage, false}, {"legacy", legacy, false}} {
				t.Run(credential.name, func(t *testing.T) {
					for _, route := range []struct {
						method, path, contentType, body string
						accepted, denied                int
					}{
						{http.MethodGet, "/" + page.Slug, "", "", http.StatusOK, http.StatusFound},
						{http.MethodGet, "/" + page.Slug + "/rss", "", "", http.StatusOK, http.StatusFound},
						{http.MethodGet, fmt.Sprintf("/api/v1/status-pages/%d/public", page.ID), "", "", http.StatusOK, http.StatusUnauthorized},
						{http.MethodPost, "/" + page.Slug + "/subscribe", "application/x-www-form-urlencoded", "type=webhook&webhook_url=https%3A%2F%2F127.0.0.1%2Fform", http.StatusSeeOther, http.StatusFound},
						{http.MethodPost, fmt.Sprintf("/api/v1/status-pages/%d/subscribe", page.ID), "application/json", `{"type":"webhook","webhook_url":"https://127.0.0.1/api"}`, http.StatusCreated, http.StatusUnauthorized},
					} {
						req := httptest.NewRequest(route.method, base+route.path, strings.NewReader(route.body))
						req.Header.Set("Content-Type", route.contentType)
						req.AddCookie(&http.Cookie{Name: cookie.Name, Value: credential.value})
						w := httptest.NewRecorder()
						srv.ServeHTTP(w, req)
						want := route.denied
						if credential.valid {
							want = route.accepted
						} else if strings.Contains(w.Body.String(), page.Title) {
							t.Errorf("%s leaked protected title", route.path)
						}
						if w.Code != want {
							t.Errorf("%s %s: status %d, want %d: %s", route.method, route.path, w.Code, want, w.Body.String())
						}
						if !credential.valid && route.denied == http.StatusFound && w.Header().Get("Location") != base+"/"+page.Slug+"/auth" {
							t.Errorf("authentication redirect has wrong base path: %q", w.Header().Get("Location"))
						}
					}
				})
			}
			w = httptest.NewRecorder()
			srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+"/"+page.Slug+"/auth", nil))
			if w.Code != http.StatusOK || strings.Contains(w.Body.String(), page.Title) || !strings.Contains(w.Body.String(), "This status page is password protected.") {
				t.Errorf("unauthenticated password form discloses private branding or fails: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

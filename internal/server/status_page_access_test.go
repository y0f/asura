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

	"github.com/y0f/asura/v2/internal/storage"
)

func createTestStatusPage(t *testing.T, srv *Server, password string) *storage.StatusPage {
	t.Helper()
	page := &storage.StatusPage{Title: "Service status", Slug: "service-status", Enabled: true, APIEnabled: true}
	if password != "" {
		page.PasswordHash = fmt.Sprintf("%x", sha256.Sum256([]byte(password)))
	}
	if err := srv.store.CreateStatusPage(context.Background(), page); err != nil {
		t.Fatal(err)
	}
	srv.refreshStatusSlugs()
	return page
}

func statusPageCookie(t *testing.T, srv *Server, page *storage.StatusPage, password string) *http.Cookie {
	t.Helper()
	form := url.Values{"password": {password}}
	req := httptest.NewRequest(http.MethodPost, "/"+page.Slug+"/auth", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther || len(w.Result().Cookies()) != 1 {
		t.Fatalf("status page login: %d %s", w.Code, w.Body.String())
	}
	return w.Result().Cookies()[0]
}

func TestSubscriptionRespectsStatusPageAccess(t *testing.T) {
	for _, tc := range []struct {
		name       string
		password   string
		header     string
		cookie     bool
		badCookie  bool
		disabled   bool
		apiOff     bool
		wantStatus int
	}{
		{name: "public", wantStatus: http.StatusCreated},
		{name: "password required", password: "page-secret", wantStatus: http.StatusUnauthorized},
		{name: "wrong password", password: "page-secret", header: "incorrect", wantStatus: http.StatusUnauthorized},
		{name: "password header", password: "page-secret", header: "page-secret", wantStatus: http.StatusCreated},
		{name: "web login cookie", password: "page-secret", cookie: true, wantStatus: http.StatusCreated},
		{name: "invalid cookie", password: "page-secret", badCookie: true, wantStatus: http.StatusUnauthorized},
		{name: "header overrides cookie", password: "page-secret", header: "incorrect", cookie: true, wantStatus: http.StatusUnauthorized},
		{name: "disabled page", disabled: true, wantStatus: http.StatusNotFound},
		{name: "disabled API", apiOff: true, wantStatus: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := testServer(t)
			t.Cleanup(srv.Close)
			srv.cfg.Monitor.AllowPrivateTargets = true
			srv.cfg.Subscriptions.Enabled = true
			page := createTestStatusPage(t, srv, tc.password)
			page.Enabled = !tc.disabled
			page.APIEnabled = !tc.apiOff
			if err := srv.store.UpdateStatusPage(context.Background(), page); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/status-pages/%d/subscribe", page.ID), strings.NewReader(`{"type":"webhook","webhook_url":"https://127.0.0.1/subscriber-test"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Status-Page-Password", tc.header)
			if tc.cookie {
				req.AddCookie(statusPageCookie(t, srv, page, tc.password))
			}
			if tc.badCookie {
				req.AddCookie(&http.Cookie{Name: fmt.Sprintf("sp_auth_%d", page.ID), Value: "invalid"})
			}
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			count, err := srv.store.CountSubscribersByPage(context.Background(), page.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantCount := int64(0)
			if tc.wantStatus == http.StatusCreated {
				wantCount = 1
			}
			if count != wantCount {
				t.Errorf("stored subscribers = %d, want %d", count, wantCount)
			}
		})
	}
}

func TestSubscriptionPasswordAttemptsSharePageLimiter(t *testing.T) {
	srv, _ := testServer(t)
	srv.cfg.Subscriptions.Enabled = true
	t.Cleanup(srv.Close)
	page := createTestStatusPage(t, srv, "page-secret")
	for i := 0; i <= srv.cfg.Auth.Login.RateLimitBurst; i++ {
		path := fmt.Sprintf("/api/v1/status-pages/%d/subscribe", page.ID)
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		if i == srv.cfg.Auth.Login.RateLimitBurst {
			req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/status-pages/%d/public", page.ID), nil)
		}
		req.Header.Set("X-Status-Page-Password", "incorrect")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		want := http.StatusUnauthorized
		if i == srv.cfg.Auth.Login.RateLimitBurst {
			want = http.StatusTooManyRequests
		}
		if w.Code != want {
			t.Fatalf("attempt %d: status = %d, want %d: %s", i+1, w.Code, want, w.Body.String())
		}
	}
}

package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatusPageCachePolicy(t *testing.T) {
	for _, password := range []string{"", "page-secret"} {
		for _, format := range []string{"json", "rss", "html"} {
			t.Run(fmt.Sprintf("%s/protected=%t", format, password != ""), func(t *testing.T) {
				srv, _ := testServer(t)
				t.Cleanup(srv.Close)
				page := createTestStatusPage(t, srv, password)
				path, wantCache := "/"+page.Slug, "no-store"
				switch format {
				case "json":
					path, wantCache = fmt.Sprintf("/api/v1/status-pages/%d/public", page.ID), "public, max-age=30"
				case "rss":
					path, wantCache = path+"/rss", "public, max-age=300"
				}
				req := httptest.NewRequest(http.MethodGet, path, nil)
				if password != "" {
					wantCache = "no-store"
					if format == "json" {
						req.Header.Set("X-Status-Page-Password", password)
					} else {
						req.AddCookie(statusPageCookie(t, srv, page, password))
					}
				}
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("status = %d: %s", w.Code, w.Body.String())
				}
				if got := w.Header().Get("Cache-Control"); got != wantCache {
					t.Errorf("Cache-Control = %q, want %q", got, wantCache)
				}
				if password != "" {
					denied := httptest.NewRecorder()
					srv.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, path, nil))
					wantStatus := http.StatusFound
					if format == "json" {
						wantStatus = http.StatusUnauthorized
					}
					if denied.Code != wantStatus || denied.Header().Get("Cache-Control") != wantCache {
						t.Errorf("unauthenticated response = %d, %q; want %d, %q", denied.Code, denied.Header().Get("Cache-Control"), wantStatus, wantCache)
					}
				}
			})
		}
	}
}

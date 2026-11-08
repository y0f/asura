package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/httputil"
	"github.com/y0f/asura/v2/internal/pageauth"
)

func TestStatusPagePasswordWebSaveAndLoginUseSaltedFormat(t *testing.T) {
	h, store := storeHandler(t)
	h.loginRL = httputil.NewRateLimiter(100, 100)
	t.Cleanup(h.loginRL.Stop)
	form := url.Values{"title": {"Protected"}, "slug": {"protected"}, "enabled": {"on"}, "api_enabled": {"on"}, "password": {"form-password"}}
	w := httptest.NewRecorder()
	h.StatusPageCreate(w, adminRequest(http.MethodPost, "/status-pages", form))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("form create failed: %d %s", w.Code, w.Body.String())
	}
	page, err := store.GetStatusPageBySlug(context.Background(), "protected")
	if err != nil || !strings.HasPrefix(page.PasswordHash, "$asura-pbkdf2-sha256$") || !pageauth.ValidHash(page.PasswordHash) {
		t.Fatalf("form password not salted: %v", err)
	}
	w = httptest.NewRecorder()
	h.StatusPageAuthPost(w, adminRequest(http.MethodPost, "/protected/auth", url.Values{"password": {"form-password"}}), page.ID, page.Slug)
	if w.Code != http.StatusSeeOther || len(w.Result().Cookies()) != 1 {
		t.Fatalf("form-generated hash could not log in: %d", w.Code)
	}
	form.Set("password", strings.Repeat("x", pageauth.MaxPasswordBytes+1))
	r := adminRequest(http.MethodPost, "/status-pages/"+strconvI(page.ID), form)
	r.SetPathValue("id", strconvI(page.ID))
	w = httptest.NewRecorder()
	h.StatusPageUpdate(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized form password accepted: %d", w.Code)
	}
	after, err := store.GetStatusPage(context.Background(), page.ID)
	if err != nil || after.PasswordHash != page.PasswordHash {
		t.Fatalf("rejected form replacement changed protection: %v", err)
	}
}

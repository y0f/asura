package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/httputil"
)

func TestTagWriteOnlyMutationsRedirectToReadableHome(t *testing.T) {
	h, store := storeHandler(t)
	h.cfg.Server.BasePath = "/console"
	key := &config.APIKeyConfig{Permissions: []string{"tags.create", "tags.update", "tags.delete", "incidents.read"}}
	mutate := func(handler http.HandlerFunc, id string) *httptest.ResponseRecorder {
		r := adminRequest("POST", "/console/tags", url.Values{"name": {"tag"}, "color": {"#005bbf"}})
		r.SetPathValue("id", id)
		r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, key))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	if w := mutate(h.TagCreate, ""); w.Code != 303 || w.Header().Get("Location") != "/console/incidents" {
		t.Fatalf("create redirect: %d %s", w.Code, w.Header().Get("Location"))
	}
	tag, err := store.GetTagByName(context.Background(), "tag")
	if err != nil {
		t.Fatal(err)
	}
	for _, handler := range []http.HandlerFunc{h.TagUpdate, h.TagDelete} {
		if w := mutate(handler, strconvI(tag.ID)); w.Code != 303 || w.Header().Get("Location") != "/console/incidents" {
			t.Fatalf("mutation redirect: %d %s", w.Code, w.Header().Get("Location"))
		}
	}
	for _, handler := range []http.HandlerFunc{h.TagUpdate, h.TagDelete} {
		if w := mutate(handler, strconvI(tag.ID)); w.Code != 404 || strings.Contains(w.Header().Get("Set-Cookie"), "asura_flash") {
			t.Fatalf("missing mutation reported success: %d %v", w.Code, w.Header())
		}
	}
}

func TestTagDatabaseFailuresAreNotEmptyOrSuccessful(t *testing.T) {
	h, store := storeHandler(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, handler := range []http.HandlerFunc{h.Tags, h.TagCreate, h.TagUpdate, h.TagDelete} {
		r := adminRequest("POST", "/tags", url.Values{"name": {"tag"}, "color": {"#005bbf"}})
		r.SetPathValue("id", "1")
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != 500 || strings.Contains(w.Body.String(), "No tags") || strings.Contains(w.Header().Get("Set-Cookie"), "asura_flash") {
			t.Fatalf("database failure reported empty/successful state: %d %s", w.Code, w.Body)
		}
	}
}

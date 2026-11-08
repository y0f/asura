package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
)

func (s *Server) refreshStatusSlugs() {
	pages, err := s.store.ListStatusPages(context.Background())
	if err != nil {
		s.logger.Error("refresh status slugs", "error", err)
		return
	}
	slugs := make(map[string]int64, len(pages))
	for _, p := range pages {
		if p.Enabled {
			slugs[p.Slug] = p.ID
		}
	}
	s.statusSlugsMu.Lock()
	s.statusSlugs = slugs
	s.statusSlugsMu.Unlock()
}

func (s *Server) getStatusPageIDBySlug(slug string) (int64, bool) {
	s.statusSlugsMu.RLock()
	defer s.statusSlugsMu.RUnlock()
	id, ok := s.statusSlugs[slug]
	return id, ok
}

// subscriberLink resolves token links even after publication is disabled. The
// mutation itself binds page/token and decides whether confirmation is allowed.
func (s *Server) subscriberLink(h func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.serveSubscriberLink(w, r, r.PathValue("slug"), h)
	}
}

func (s *Server) serveSubscriberLink(w http.ResponseWriter, r *http.Request, slug string, h func(http.ResponseWriter, *http.Request, int64)) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	page, err := s.store.GetStatusPageBySlug(r.Context(), slug)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "status page not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load status page")
		return
	}
	h(w, r, page.ID)
}

func (s *Server) statusPageRouter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.web != nil {
			path := r.URL.Path
			prefix := s.cfg.Server.BasePath + "/"
			if strings.HasPrefix(path, prefix) {
				rest := strings.TrimPrefix(path, prefix)
				rest = strings.TrimSuffix(rest, "/")

				slug := rest
				suffix := ""
				if idx := strings.Index(rest, "/"); idx != -1 {
					slug = rest[:idx]
					suffix = rest[idx+1:]
				}

				if slug != "" && !strings.Contains(slug, "/") {
					// Token actions need fresh page identity even after a page is
					// disabled and disappears from the public-route cache.
					if r.Method == http.MethodGet && suffix == "confirm" {
						s.serveSubscriberLink(w, r, slug, s.web.StatusPageConfirm)
						return
					}
					if r.Method == http.MethodGet && suffix == "unsubscribe" {
						s.serveSubscriberLink(w, r, slug, s.web.StatusPageUnsubscribe)
						return
					}
					if pageID, ok := s.getStatusPageIDBySlug(slug); ok {
						switch {
						case r.Method == http.MethodGet && suffix == "":
							s.web.StatusPageByID(w, r, pageID)
							return
						case r.Method == http.MethodGet && suffix == "auth":
							s.web.StatusPageAuthGet(w, r, pageID)
							return
						case r.Method == http.MethodPost && suffix == "auth":
							s.web.StatusPageAuthPost(w, r, pageID, slug)
							return
						case r.Method == http.MethodGet && suffix == "rss":
							s.web.StatusPageRSS(w, r, pageID)
							return
						case r.Method == http.MethodPost && suffix == "subscribe":
							s.web.StatusPageSubscribe(w, r, pageID)
							return
						}
					}
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

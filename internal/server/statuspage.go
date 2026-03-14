package server

import (
	"context"
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
						case r.Method == http.MethodPost && suffix == "subscribe":
							s.web.StatusPageSubscribe(w, r, pageID)
							return
						case r.Method == http.MethodGet && suffix == "confirm":
							s.web.StatusPageConfirm(w, r, pageID)
							return
						case r.Method == http.MethodGet && suffix == "unsubscribe":
							s.web.StatusPageUnsubscribe(w, r, pageID)
							return
						}
					}
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

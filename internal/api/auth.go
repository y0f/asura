package api

import (
	"context"
	"net/http"

	"github.com/y0f/asura/internal/httputil"
)

// Auth authenticates the key and requires every scope. With no scopes, the
// handler must enforce its own authorization (for example a bulk action).
func (h *Handler) Auth(scopes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("X-API-Key")
			if key == "" {
				writeError(w, http.StatusUnauthorized, "missing API key")
				return
			}

			apiKey, ok := h.cfg.LookupAPIKey(key)
			if !ok {
				writeError(w, http.StatusUnauthorized, "invalid API key")
				return
			}

			if !apiKey.HasAllPermissions(scopes...) {
				writeError(w, http.StatusForbidden, "forbidden")
				return
			}

			ctx := context.WithValue(r.Context(), httputil.CtxKeyAPIKey, apiKey)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/y0f/asura/internal/httputil"
)

func recovery(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logger.Error("panic recovered", "error", fmt.Sprintf("%v", err), "path", r.URL.Path)
					writeError(w, http.StatusInternalServerError, "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func requestID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := httputil.GenerateID()
			ctx := context.WithValue(r.Context(), httputil.CtxKeyRequestID, id)
			w.Header().Set("X-Request-ID", id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func logging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &httputil.StatusWriter{ResponseWriter: w, Code: 200}
			next.ServeHTTP(sw, r)
			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.Code,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", httputil.GetRequestID(r.Context()),
				"remote", r.RemoteAddr,
			)
		})
	}
}

func buildFrameAncestorsDirective(ancestors []string) string {
	if len(ancestors) == 0 {
		return "frame-ancestors 'none'"
	}
	parts := make([]string, len(ancestors))
	for i, a := range ancestors {
		if a == "self" {
			parts[i] = "'self'"
		} else {
			parts[i] = a
		}
	}
	return "frame-ancestors " + strings.Join(parts, " ")
}

func secureHeaders(frameAncestors []string) func(http.Handler) http.Handler {
	var xFrameOptions string
	switch {
	case len(frameAncestors) == 0:
		xFrameOptions = "DENY"
	case len(frameAncestors) == 1 && frameAncestors[0] == "self":
		xFrameOptions = "SAMEORIGIN"
	}

	cspFrame := buildFrameAncestorsDirective(frameAncestors)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if xFrameOptions != "" {
				w.Header().Set("X-Frame-Options", xFrameOptions)
			}
			w.Header().Set("X-XSS-Protection", "0")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; "+cspFrame)
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			w.Header().Set("Cache-Control", "no-store")
			if r.TLS != nil {
				w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

func cors(allowedOrigins []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && isAllowedOrigin(origin, allowedOrigins) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key")
				w.Header().Set("Access-Control-Max-Age", "86400")
				w.Header().Set("Vary", "Origin")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isAllowedOrigin(origin string, allowed []string) bool {
	for _, a := range allowed {
		if a == "*" || a == origin {
			return true
		}
	}
	return false
}

func bodyLimit(maxBytes int64, exemptPaths ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && !isExemptPath(r.URL.Path, exemptPaths) {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isExemptPath(path string, exempt []string) bool {
	for _, p := range exempt {
		if path == p {
			return true
		}
	}
	return false
}

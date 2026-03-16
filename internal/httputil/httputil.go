package httputil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/incident"
	"github.com/y0f/asura/internal/storage"
	"golang.org/x/time/rate"
)

type ContextKey string

const (
	CtxKeyRequestID ContextKey = "request_id"
	CtxKeyAPIKey    ContextKey = "api_key"
)

func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(CtxKeyRequestID).(string); ok {
		return id
	}
	return ""
}

func GetAPIKeyName(ctx context.Context) string {
	if k, ok := ctx.Value(CtxKeyAPIKey).(*config.APIKeyConfig); ok {
		return k.Name
	}
	return ""
}

func GetAPIKey(ctx context.Context) *config.APIKeyConfig {
	if k, ok := ctx.Value(CtxKeyAPIKey).(*config.APIKeyConfig); ok {
		return k
	}
	return nil
}

func GenerateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

type StatusWriter struct {
	http.ResponseWriter
	Code int
}

func (w *StatusWriter) WriteHeader(code int) {
	w.Code = code
	w.ResponseWriter.WriteHeader(code)
}

func ParsePagination(r *http.Request) storage.Pagination {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	return storage.Pagination{Page: page, PerPage: perPage}
}

func ParseID(r *http.Request) (int64, error) {
	idStr := r.PathValue("id")
	if idStr == "" {
		return 0, fmt.Errorf("missing id parameter")
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid id: %s", idStr)
	}
	return id, nil
}

func ExtractIP(r *http.Request, trustedNets []net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	remoteIP := net.ParseIP(host)
	if remoteIP == nil || !IsTrusted(remoteIP, trustedNets) {
		return host
	}

	if cfIP := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cfIP != "" {
		if net.ParseIP(cfIP) != nil {
			return cfIP
		}
	}

	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		if net.ParseIP(realIP) != nil {
			return realIP
		}
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			ip := strings.TrimSpace(parts[i])
			parsed := net.ParseIP(ip)
			if parsed == nil {
				continue
			}
			if !IsTrusted(parsed, trustedNets) {
				return ip
			}
		}
		return strings.TrimSpace(parts[0])
	}

	return host
}

func IsTrusted(ip net.IP, nets []net.IPNet) bool {
	for i := range nets {
		if nets[i].Contains(ip) {
			return true
		}
	}
	return false
}

type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitorEntry
	rate     rate.Limit
	burst    int
	done     chan struct{}
}

type visitorEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func NewRateLimiter(rps float64, burst int) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitorEntry),
		rate:     rate.Limit(rps),
		burst:    burst,
		done:     make(chan struct{}),
	}
	go rl.cleanup()
	return rl
}

func (rl *RateLimiter) Stop() {
	close(rl.done)
}

func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-rl.done:
			return
		case <-ticker.C:
			rl.mu.Lock()
			for ip, v := range rl.visitors {
				if time.Since(v.lastSeen) > 3*time.Minute {
					delete(rl.visitors, ip)
				}
			}
			rl.mu.Unlock()
		}
	}
}

func (rl *RateLimiter) GetLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[ip]
	if !exists {
		limiter := rate.NewLimiter(rl.rate, rl.burst)
		rl.visitors[ip] = &visitorEntry{limiter: limiter, lastSeen: time.Now()}
		return limiter
	}
	v.lastSeen = time.Now()
	return v.limiter
}

func (rl *RateLimiter) Allow(ip string) bool {
	return rl.GetLimiter(ip).Allow()
}

func (rl *RateLimiter) Middleware(trustedNets []net.IPNet, writeError func(http.ResponseWriter, int, string)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := ExtractIP(r, trustedNets)
			if !rl.GetLimiter(ip).Allow() {
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func OverallStatus(monitors []*storage.Monitor) string {
	overall := "operational"
	for _, m := range monitors {
		if m.Status == "down" {
			return "major_outage"
		}
		if m.Status == "degraded" {
			overall = "degraded"
		}
	}
	return overall
}

func PublicIncidentsForPage(ctx context.Context, store storage.Store, sp *storage.StatusPage, monitors []*storage.Monitor, now time.Time) []*storage.Incident {
	if !sp.ShowIncidents {
		return []*storage.Incident{}
	}

	monitorIDs := make(map[int64]bool, len(monitors))
	for _, m := range monitors {
		monitorIDs[m.ID] = true
	}

	incResult, err := store.ListIncidents(ctx, 0, "", "", storage.Pagination{Page: 1, PerPage: 20})
	if err != nil || incResult == nil {
		return []*storage.Incident{}
	}

	all, ok := incResult.Data.([]*storage.Incident)
	if !ok {
		return []*storage.Incident{}
	}

	cutoff := now.AddDate(0, 0, -7)
	var filtered []*storage.Incident
	for _, inc := range all {
		if !monitorIDs[inc.MonitorID] {
			continue
		}
		if inc.Status == incident.StatusResolved && inc.ResolvedAt != nil && inc.ResolvedAt.Before(cutoff) {
			continue
		}
		safe := *inc
		safe.Cause = ""
		filtered = append(filtered, &safe)
		if len(filtered) >= 10 {
			break
		}
	}
	if filtered == nil {
		filtered = []*storage.Incident{}
	}
	return filtered
}

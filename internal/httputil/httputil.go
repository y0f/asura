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

	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/storage"
	"golang.org/x/time/rate"
)

type ContextKey string

const (
	CtxKeyRequestID        ContextKey = "request_id"
	CtxKeyAPIKey           ContextKey = "api_key"
	CtxKeySessionTokenHash ContextKey = "session_token_hash"
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

// Flush forwards to the underlying ResponseWriter so wrapping in StatusWriter
// doesn't strip http.Flusher — required for streaming responses (SSE).
func (w *StatusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (w *StatusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
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

// ExtractIP returns the client address for rate limiting, logs and audit.
// Forwarding headers are honoured only from a trusted proxy. The proxy
// appends the address it received the request from to X-Forwarded-For, so
// the list is walked from the right past trusted proxies; entries further left
// are client-supplied and never used. X-Real-IP is used only when no
// X-Forwarded-For is present. Vendor headers such as CF-Connecting-IP are
// ignored because ordinary proxies pass them through from the client.
func ExtractIP(r *http.Request, trustedNets []net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	remoteIP := net.ParseIP(host)
	if remoteIP == nil || !IsTrusted(remoteIP, trustedNets) {
		return host
	}

	if values := r.Header.Values("X-Forwarded-For"); len(values) > 0 {
		client := host
		parts := strings.Split(strings.Join(values, ","), ",")
		for i := len(parts) - 1; i >= 0; i-- {
			parsed := net.ParseIP(strings.TrimSpace(parts[i]))
			if parsed == nil {
				// A trusted proxy only appends valid addresses; anything else
				// came from the client. Keep the last address a proxy vouched for.
				return client
			}
			client = parsed.String()
			if !IsTrusted(parsed, trustedNets) {
				return client
			}
		}
		return client
	}

	if realIP := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); realIP != nil {
		return realIP.String()
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

// maxRateLimitVisitors bounds limiter memory. Once full, new clients share a
// single overflow bucket instead of growing the map, so address rotation can
// neither exhaust memory nor obtain a fresh budget per address.
const maxRateLimitVisitors = 100000

const overflowVisitorKey = "overflow" // never a valid address or prefix

// RateLimitKey groups a client address into the unit one client controls:
// an IPv4 address or an IPv6 /64, the smallest prefix normally assigned to a
// single host or subscriber. Unparseable values are used as given.
func RateLimitKey(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ip
	}
	if v4 := parsed.To4(); v4 != nil {
		return v4.String()
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

func (rl *RateLimiter) GetLimiter(ip string) *rate.Limiter {
	key := RateLimitKey(ip)
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	v, exists := rl.visitors[key]
	if !exists {
		if len(rl.visitors) >= maxRateLimitVisitors {
			key = overflowVisitorKey
			if v, exists = rl.visitors[key]; exists {
				v.lastSeen = now
				return v.limiter
			}
		}
		limiter := rate.NewLimiter(rl.rate, rl.burst)
		rl.visitors[key] = &visitorEntry{limiter: limiter, lastSeen: now}
		return limiter
	}
	v.lastSeen = now
	return v.limiter
}

func (rl *RateLimiter) Allow(ip string) bool {
	return rl.GetLimiter(ip).Allow()
}

// LimitFailures runs attempt unless ip is rate limited and charges ip a token
// only if attempt fails, so successful attempts never use up the limit. It
// reports whether attempt succeeded and whether ip was limited, in which case
// attempt is not run at all: a throttled client cannot test a guess.
func (rl *RateLimiter) LimitFailures(ip string, attempt func() bool) (ok, limited bool) {
	lim := rl.GetLimiter(ip)
	if lim.Tokens() < 1 {
		return false, true
	}
	if attempt() {
		return true, false
	}
	// Reserve, unlike Allow, always charges the token: concurrent failures
	// may overdraw the bucket, which then delays the client's next attempts.
	lim.Reserve()
	return false, false
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
	pending := len(monitors) == 0
	for _, m := range monitors {
		switch m.Status {
		case "down":
			return "major_outage"
		case "degraded":
			overall = "degraded"
		case "up":
		default:
			pending = true
		}
	}
	if overall == "operational" && pending {
		return "unknown"
	}
	return overall
}

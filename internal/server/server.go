package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/y0f/asura/internal/api"
	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/monitor"
	"github.com/y0f/asura/internal/notifier"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/web"
)

var _ http.Handler = (*Server)(nil)

type Server struct {
	cfg           *config.Config
	store         storage.Store
	pipeline      *monitor.Pipeline
	notifier      *notifier.Dispatcher
	logger        *slog.Logger
	api           *api.Handler
	web           *web.Handler
	handler       http.Handler
	rateLimiter   *httputil.RateLimiter
	loginRL       *httputil.RateLimiter
	reqLogWriter  *RequestLogWriter
	events        *EventBroker
	statusSlugsMu sync.RWMutex
	statusSlugs   map[string]int64
	version       string
}

func NewServer(cfg *config.Config, store storage.Store, pipeline *monitor.Pipeline, dispatcher *notifier.Dispatcher, subNotifier *notifier.SubscriberNotifier, logger *slog.Logger, version string) *Server {
	s := &Server{
		cfg:          cfg,
		store:        store,
		pipeline:     pipeline,
		notifier:     dispatcher,
		logger:       logger,
		version:      version,
		reqLogWriter: NewRequestLogWriter(store, logger),
		events:       NewEventBroker(logger),
		statusSlugs:  make(map[string]int64),
	}

	s.events.authorize = s.authorizeEvent

	cspDirective := buildFrameAncestorsDirective(cfg.Server.FrameAncestors)

	// One login limiter for the API and the web UI, so status page password
	// guesses through the API header and the web form share one budget.
	s.loginRL = httputil.NewRateLimiter(cfg.Auth.Login.RateLimitPerSec, cfg.Auth.Login.RateLimitBurst)
	s.api = api.New(cfg, store, pipeline, dispatcher, logger, s.loginRL)
	s.api.OnStatusPageChange = s.refreshStatusSlugs
	s.api.SubNotifier = subNotifier

	if cfg.IsWebUIEnabled() {
		s.web = web.New(cfg, store, pipeline, dispatcher, subNotifier, logger, version, cspDirective, s.loginRL)
		s.web.OnStatusPageChange = s.refreshStatusSlugs
	}

	s.refreshStatusSlugs()

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	var handler http.Handler = apiJSONErrors(mux, s.p("/api/v1/"))
	if cfg.IsWebUIEnabled() {
		handler = s.statusPageRouter(handler)
	}
	// Transfer handlers apply their own authenticated, bounded JSON/multipart
	// limits before parsing. A normal small request cap cannot round-trip files.
	handler = bodyLimit(cfg.Server.MaxBodySize, s.p("/api/v1/agent/results"), s.p("/api/v1/import"), s.p("/settings/import"))(handler)
	s.rateLimiter = httputil.NewRateLimiter(cfg.Server.RateLimitPerSec, cfg.Server.RateLimitBurst)
	handler = s.rateLimiter.Middleware(cfg.TrustedNets(), writeError)(handler)
	handler = cors(cfg.Server.CORSOrigins)(handler)
	handler = secureHeaders(cfg.Server.FrameAncestors)(handler)
	handler = requestLogMiddleware(s.reqLogWriter, cfg.Server.BasePath, cfg.TrustedNets())(handler)
	handler = logging(logger)(handler)
	handler = requestID()(handler)
	handler = recovery(logger)(handler)

	s.handler = handler
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// Close stops the background goroutines of the server's rate limiters and
// handlers. Call it once, after the HTTP server has shut down.
func (s *Server) Close() {
	s.rateLimiter.Stop()
	s.loginRL.Stop()
	if s.web != nil {
		s.web.Stop()
	}
}

func (s *Server) RequestLogWriter() *RequestLogWriter {
	return s.reqLogWriter
}

func (s *Server) EventBroker() *EventBroker {
	return s.events
}

func (s *Server) p(path string) string {
	return s.cfg.Server.BasePath + path
}

// apiJSONErrors replaces the mux's plain-text 404 and 405 replies with the
// API's JSON error format for paths under prefix.
func apiJSONErrors(mux *http.ServeMux, prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, prefix) {
			if h, pattern := mux.Handler(r); pattern == "" {
				// No route matched: h is the mux's 404 or 405 handler. Run it
				// against a discarding writer to learn which, and keep Allow.
				rec := &statusRecorder{header: http.Header{}}
				h.ServeHTTP(rec, r)
				if allow := rec.header.Get("Allow"); allow != "" {
					w.Header().Set("Allow", allow)
				}
				code := rec.code
				if code == 0 {
					code = http.StatusNotFound
				}
				writeError(w, code, strings.ToLower(http.StatusText(code)))
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	header http.Header
	code   int
}

func (s *statusRecorder) Header() http.Header         { return s.header }
func (s *statusRecorder) Write(p []byte) (int, error) { return len(p), nil }
func (s *statusRecorder) WriteHeader(code int)        { s.code = code }

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

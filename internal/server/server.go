package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
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
	reqLogWriter  *RequestLogWriter
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
		statusSlugs:  make(map[string]int64),
	}

	cspDirective := buildFrameAncestorsDirective(cfg.Server.FrameAncestors)

	s.api = api.New(cfg, store, pipeline, dispatcher, logger)
	s.api.OnStatusPageChange = s.refreshStatusSlugs

	if cfg.IsWebUIEnabled() {
		s.web = web.New(cfg, store, pipeline, dispatcher, subNotifier, logger, version, cspDirective)
		s.web.OnStatusPageChange = s.refreshStatusSlugs
	}

	s.refreshStatusSlugs()

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	var handler http.Handler = mux
	if cfg.IsWebUIEnabled() {
		handler = s.statusPageRouter(handler)
	}
	handler = bodyLimit(cfg.Server.MaxBodySize)(handler)
	rl := httputil.NewRateLimiter(cfg.Server.RateLimitPerSec, cfg.Server.RateLimitBurst)
	handler = rl.Middleware(cfg.TrustedNets(), writeError)(handler)
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

func (s *Server) RequestLogWriter() *RequestLogWriter {
	return s.reqLogWriter
}

func (s *Server) p(path string) string {
	return s.cfg.Server.BasePath + path
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

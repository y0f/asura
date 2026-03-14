package web

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/a-h/templ"
	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/monitor"
	"github.com/y0f/asura/internal/notifier"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/web/views"
)

type Handler struct {
	cfg                *config.Config
	store              storage.Store
	pipeline           *monitor.Pipeline
	notifier           *notifier.Dispatcher
	subNotifier        *notifier.SubscriberNotifier
	logger             *slog.Logger
	version            string
	cspFrameDirective  string
	OnStatusPageChange func()
	loginRL            *httputil.RateLimiter
	totpMu             sync.Mutex
	totpChallenges     map[string]*totpChallenge
	done               chan struct{}
}

func (h *Handler) Stop() {
	close(h.done)
	h.loginRL.Stop()
}

func New(cfg *config.Config, store storage.Store, pipeline *monitor.Pipeline,
	dispatcher *notifier.Dispatcher, subNotifier *notifier.SubscriberNotifier,
	logger *slog.Logger, version, cspDirective string) *Handler {
	h := &Handler{
		cfg:               cfg,
		store:             store,
		pipeline:          pipeline,
		notifier:          dispatcher,
		subNotifier:       subNotifier,
		logger:            logger,
		version:           version,
		cspFrameDirective: cspDirective,
		loginRL:           httputil.NewRateLimiter(cfg.Auth.Login.RateLimitPerSec, cfg.Auth.Login.RateLimitBurst),
		totpChallenges:    make(map[string]*totpChallenge),
		done:              make(chan struct{}),
	}
	go h.cleanupTOTPChallenges()
	return h
}

func (h *Handler) newLayoutParams(r *http.Request, title, active string) views.LayoutParams {
	perms := make(map[string]bool)
	if k := httputil.GetAPIKey(r.Context()); k != nil {
		perms = k.PermissionMap()
	}
	toastKind, toastMsg := "", ""
	if c, err := r.Cookie("toast"); err == nil {
		raw, _ := url.QueryUnescape(c.Value)
		if idx := strings.Index(raw, ":"); idx > 0 {
			toastKind = raw[:idx]
			toastMsg = raw[idx+1:]
		} else {
			toastKind = "success"
			toastMsg = raw
		}
	}
	return views.LayoutParams{
		Title:     title,
		Active:    active,
		Perms:     perms,
		Version:   h.version,
		ToastKind: toastKind,
		ToastMsg:  toastMsg,
		BasePath:  h.cfg.Server.BasePath,
	}
}

func (h *Handler) renderComponent(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; "+h.cspFrameDirective)
	if err := c.Render(r.Context(), w); err != nil {
		h.logger.Error("templ render", "error", err)
	}
}

func (h *Handler) redirect(w http.ResponseWriter, r *http.Request, path string) {
	http.Redirect(w, r, h.cfg.Server.BasePath+path, http.StatusSeeOther)
}

func (h *Handler) setToast(w http.ResponseWriter, kind, msg string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "toast",
		Value:    url.QueryEscape(kind + ":" + msg),
		Path:     h.cfg.Server.BasePath + "/",
		MaxAge:   5,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) setFlash(w http.ResponseWriter, msg string) {
	h.setToast(w, "success", msg)
}

func (h *Handler) setError(w http.ResponseWriter, msg string) {
	h.setToast(w, "error", msg)
}

func (h *Handler) audit(r *http.Request, action, entity string, entityID int64, detail string) {
	entry := &storage.AuditEntry{
		Action:     action,
		Entity:     entity,
		EntityID:   entityID,
		APIKeyName: httputil.GetAPIKeyName(r.Context()),
		Detail:     detail,
	}
	if err := h.store.InsertAudit(r.Context(), entry); err != nil {
		h.logger.Error("audit log failed", "error", err)
	}
}

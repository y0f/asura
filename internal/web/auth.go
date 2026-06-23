package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/totp"
	"github.com/y0f/asura/internal/web/views"
)

const sessionCookie = "asura_session"

func hashSessionToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func generateSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	h.renderComponent(w, r, views.LoginPage(views.LoginParams{BasePath: h.cfg.Server.BasePath}))
}

func (h *Handler) LoginPost(w http.ResponseWriter, r *http.Request) {
	ip := httputil.ExtractIP(r, h.cfg.TrustedNets())

	if !h.loginRL.Allow(ip) {
		h.auditLogin("login_rate_limited", "", ip)
		h.renderComponent(w, r, views.LoginPage(views.LoginParams{BasePath: h.cfg.Server.BasePath, Error: "Too many login attempts. Try again later."}))
		return
	}

	key := r.FormValue("api_key")
	if key == "" {
		h.renderComponent(w, r, views.LoginPage(views.LoginParams{BasePath: h.cfg.Server.BasePath, Error: "API key is required"}))
		return
	}

	apiKey, ok := h.cfg.LookupAPIKey(key)
	if !ok {
		h.auditLogin("login_failed", "", ip)
		h.renderComponent(w, r, views.LoginPage(views.LoginParams{BasePath: h.cfg.Server.BasePath, Error: "Invalid API key"}))
		return
	}

	if apiKey.TOTP {
		_, err := h.store.GetTOTPKey(r.Context(), apiKey.Name)
		if err != nil {
			h.renderComponent(w, r, views.LoginPage(views.LoginParams{
				BasePath: h.cfg.Server.BasePath,
				Error:    "TOTP enabled but not configured. Run: asura --setup-totp " + apiKey.Name,
			}))
			return
		}
		token, err := h.createTOTPChallenge(apiKey.Name, apiKey.Hash, ip)
		if err != nil {
			h.logger.Error("create totp challenge", "error", err)
			h.renderComponent(w, r, views.LoginPage(views.LoginParams{BasePath: h.cfg.Server.BasePath, Error: "Internal error"}))
			return
		}
		h.renderComponent(w, r, views.TOTPPage(views.TOTPParams{
			BasePath:       h.cfg.Server.BasePath,
			ChallengeToken: token,
		}))
		return
	}

	if h.cfg.Auth.TOTP.Required {
		h.renderComponent(w, r, views.LoginPage(views.LoginParams{
			BasePath: h.cfg.Server.BasePath,
			Error:    "Two-factor authentication is required. Contact your administrator.",
		}))
		return
	}

	h.createSessionAndLogin(w, r, apiKey, ip)
}

type totpChallenge struct {
	apiKeyName string
	keyHash    string
	ipAddress  string
	createdAt  time.Time
}

func (h *Handler) createTOTPChallenge(apiKeyName, keyHash, ip string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)

	h.totpMu.Lock()
	h.totpChallenges[token] = &totpChallenge{
		apiKeyName: apiKeyName,
		keyHash:    keyHash,
		ipAddress:  ip,
		createdAt:  time.Now(),
	}
	h.totpMu.Unlock()
	return token, nil
}

func (h *Handler) consumeTOTPChallenge(token string) *totpChallenge {
	h.totpMu.Lock()
	defer h.totpMu.Unlock()
	ch, ok := h.totpChallenges[token]
	if !ok {
		return nil
	}
	delete(h.totpChallenges, token)
	if time.Since(ch.createdAt) > 5*time.Minute {
		return nil
	}
	return ch
}

func (h *Handler) cleanupTOTPChallenges() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-h.done:
			return
		case <-ticker.C:
			h.totpMu.Lock()
			for k, ch := range h.totpChallenges {
				if time.Since(ch.createdAt) > 5*time.Minute {
					delete(h.totpChallenges, k)
				}
			}
			h.totpMu.Unlock()
		}
	}
}

func (h *Handler) TOTPLogin(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, h.cfg.Server.BasePath+"/login", http.StatusSeeOther)
}

func (h *Handler) TOTPLoginPost(w http.ResponseWriter, r *http.Request) {
	ip := httputil.ExtractIP(r, h.cfg.TrustedNets())

	if !h.loginRL.Allow(ip) {
		h.auditLogin("login_rate_limited", "", ip)
		h.renderComponent(w, r, views.LoginPage(views.LoginParams{BasePath: h.cfg.Server.BasePath, Error: "Too many login attempts. Try again later."}))
		return
	}

	challengeToken := r.FormValue("challenge")
	code := r.FormValue("code")

	ch := h.consumeTOTPChallenge(challengeToken)
	if ch == nil {
		h.renderComponent(w, r, views.LoginPage(views.LoginParams{BasePath: h.cfg.Server.BasePath, Error: "Session expired. Please sign in again."}))
		return
	}

	apiKey := h.cfg.LookupAPIKeyByName(ch.apiKeyName)
	if apiKey == nil || apiKey.Hash != ch.keyHash {
		h.renderComponent(w, r, views.LoginPage(views.LoginParams{BasePath: h.cfg.Server.BasePath, Error: "API key no longer valid. Please sign in again."}))
		return
	}

	totpKey, err := h.store.GetTOTPKey(r.Context(), apiKey.Name)
	if err != nil {
		h.renderComponent(w, r, views.LoginPage(views.LoginParams{BasePath: h.cfg.Server.BasePath, Error: "TOTP configuration error."}))
		return
	}

	secret, err := totp.DecodeSecret(totpKey.Secret)
	if err != nil {
		h.logger.Error("decode totp secret", "error", err)
		h.renderComponent(w, r, views.LoginPage(views.LoginParams{BasePath: h.cfg.Server.BasePath, Error: "Internal error"}))
		return
	}

	matchedCounter, ok := totp.ValidateWithCounter(secret, code, time.Now())
	if !ok {
		h.auditLogin("login_totp_failed", apiKey.Name, ip)
		newToken, err := h.createTOTPChallenge(apiKey.Name, apiKey.Hash, ip)
		if err != nil {
			h.logger.Error("create totp challenge", "error", err)
			h.renderComponent(w, r, views.LoginPage(views.LoginParams{BasePath: h.cfg.Server.BasePath, Error: "Internal error"}))
			return
		}
		h.renderComponent(w, r, views.TOTPPage(views.TOTPParams{
			BasePath:       h.cfg.Server.BasePath,
			Error:          "Invalid code. Please try again.",
			ChallengeToken: newToken,
		}))
		return
	}

	// Atomic compare-and-swap: only accept the code if its time-step is strictly
	// greater than the last accepted one. This closes the TOCTOU window so two
	// concurrent logins reusing the same code cannot both succeed.
	fresh, err := h.store.AdvanceTOTPCounter(r.Context(), apiKey.Name, matchedCounter)
	if err != nil {
		h.logger.Error("advance totp counter", "error", err)
		h.renderComponent(w, r, views.LoginPage(views.LoginParams{BasePath: h.cfg.Server.BasePath, Error: "Internal error"}))
		return
	}
	if !fresh {
		h.auditLogin("login_totp_replay", apiKey.Name, ip)
		newToken, err := h.createTOTPChallenge(apiKey.Name, apiKey.Hash, ip)
		if err != nil {
			h.logger.Error("create totp challenge", "error", err)
			h.renderComponent(w, r, views.LoginPage(views.LoginParams{BasePath: h.cfg.Server.BasePath, Error: "Internal error"}))
			return
		}
		h.renderComponent(w, r, views.TOTPPage(views.TOTPParams{
			BasePath:       h.cfg.Server.BasePath,
			Error:          "This code has already been used. Wait for a new code.",
			ChallengeToken: newToken,
		}))
		return
	}

	h.auditLogin("login_success_totp", apiKey.Name, ip)
	h.createSessionAndLogin(w, r, apiKey, ip)
}

func (h *Handler) createSessionAndLogin(w http.ResponseWriter, r *http.Request, apiKey *config.APIKeyConfig, ip string) {
	token, err := generateSessionToken()
	if err != nil {
		h.logger.Error("generate session token", "error", err)
		h.renderComponent(w, r, views.LoginPage(views.LoginParams{BasePath: h.cfg.Server.BasePath, Error: "Internal error"}))
		return
	}

	sess := &storage.Session{
		TokenHash:  hashSessionToken(token),
		APIKeyName: apiKey.Name,
		KeyHash:    apiKey.Hash,
		IPAddress:  ip,
		ExpiresAt:  time.Now().Add(h.cfg.Auth.Session.Lifetime),
	}
	if err := h.store.CreateSession(r.Context(), sess); err != nil {
		h.logger.Error("create session", "error", err)
		h.renderComponent(w, r, views.LoginPage(views.LoginParams{BasePath: h.cfg.Server.BasePath, Error: "Internal error"}))
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     h.cfg.Server.BasePath + "/",
		MaxAge:   int(h.cfg.Auth.Session.Lifetime.Seconds()),
		HttpOnly: true,
		Secure:   h.cfg.Auth.Session.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})

	h.auditLogin("login_success", apiKey.Name, ip)
	http.Redirect(w, r, h.cfg.Server.BasePath+"/", http.StatusSeeOther)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if !h.checkOrigin(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		tokenHash := hashSessionToken(cookie.Value)
		if err := h.store.DeleteSession(r.Context(), tokenHash); err != nil {
			h.logger.Error("web: delete session on logout", "error", err)
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     h.cfg.Server.BasePath + "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cfg.Auth.Session.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, h.cfg.Server.BasePath+"/login", http.StatusSeeOther)
}

func (h *Handler) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     h.cfg.Server.BasePath + "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cfg.Auth.Session.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) RequireAuth(next http.Handler) http.Handler {
	loginURL := h.cfg.Server.BasePath + "/login"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil || cookie.Value == "" {
			http.Redirect(w, r, loginURL, http.StatusSeeOther)
			return
		}

		tokenHash := hashSessionToken(cookie.Value)
		sess, err := h.store.GetSessionByTokenHash(r.Context(), tokenHash)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				h.clearSessionCookie(w)
				http.Redirect(w, r, loginURL, http.StatusSeeOther)
				return
			}
			h.logger.Error("session lookup", "error", err)
			h.clearSessionCookie(w)
			http.Redirect(w, r, loginURL, http.StatusSeeOther)
			return
		}

		now := time.Now()
		if now.After(sess.ExpiresAt) {
			if err := h.store.DeleteSession(r.Context(), tokenHash); err != nil {
				h.logger.Error("web: delete expired session", "error", err)
			}
			h.clearSessionCookie(w)
			http.Redirect(w, r, loginURL, http.StatusSeeOther)
			return
		}

		apiKey := h.cfg.LookupAPIKeyByName(sess.APIKeyName)
		if apiKey == nil {
			if err := h.store.DeleteSession(r.Context(), tokenHash); err != nil {
				h.logger.Error("web: delete orphaned session", "error", err)
			}
			h.clearSessionCookie(w)
			http.Redirect(w, r, loginURL, http.StatusSeeOther)
			return
		}

		if sess.KeyHash != "" && sess.KeyHash != apiKey.Hash {
			if err := h.store.DeleteSession(r.Context(), tokenHash); err != nil {
				h.logger.Error("web: delete rotated session", "error", err)
			}
			h.clearSessionCookie(w)
			h.auditLogin("session_key_rotated", sess.APIKeyName, httputil.ExtractIP(r, h.cfg.TrustedNets()))
			http.Redirect(w, r, loginURL, http.StatusSeeOther)
			return
		}

		lifetime := h.cfg.Auth.Session.Lifetime
		if now.After(sess.ExpiresAt.Add(-lifetime / 2)) {
			newExpiry := now.Add(lifetime)
			if err := h.store.ExtendSession(r.Context(), tokenHash, newExpiry); err != nil {
				h.logger.Error("web: extend session", "error", err)
			}
			http.SetCookie(w, &http.Cookie{
				Name:     sessionCookie,
				Value:    cookie.Value,
				Path:     h.cfg.Server.BasePath + "/",
				MaxAge:   int(lifetime.Seconds()),
				HttpOnly: true,
				Secure:   h.cfg.Auth.Session.CookieSecure,
				SameSite: http.SameSiteLaxMode,
			})
		}

		ctx := context.WithValue(r.Context(), httputil.CtxKeyAPIKey, apiKey)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *Handler) RequirePerm(perm string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k := httputil.GetAPIKey(r.Context())
		if k == nil || !k.HasPermission(perm) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost && !h.checkOrigin(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) auditLogin(action, keyName, ip string) {
	h.store.InsertAudit(context.Background(), &storage.AuditEntry{
		Action:     action,
		Entity:     "session",
		APIKeyName: keyName,
		Detail:     ip,
	})
}

func (h *Handler) checkOrigin(r *http.Request) bool {
	hosts := make(map[string]bool)
	hosts[stripPort(r.Host)] = true

	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		remoteHost, _, _ := net.SplitHostPort(r.RemoteAddr)
		remoteIP := net.ParseIP(remoteHost)
		if remoteIP != nil && h.cfg.IsTrustedProxy(remoteIP) {
			hosts[stripPort(fwd)] = true
		}
	}

	origin := r.Header.Get("Origin")
	if origin != "" && origin != "null" {
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return hosts[stripPort(u.Host)]
	}
	ref := r.Header.Get("Referer")
	if ref != "" {
		u, err := url.Parse(ref)
		if err != nil {
			return false
		}
		return hosts[stripPort(u.Host)]
	}
	return false
}

func stripPort(host string) string {
	if i := strings.LastIndex(host, ":"); i != -1 {
		return strings.ToLower(host[:i])
	}
	return strings.ToLower(host)
}

package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/y0f/asura/v2/internal/httputil"
)

var errStreamIdentity = errors.New("stream identity is no longer valid")

type streamIdentityKey struct{}

// Capture values rather than a mutable config pointer. A key retaining its name
// after rotation is a different credential and must not keep an existing stream.
type streamIdentity struct {
	name, keyHash, sessionHash string
}

func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request) {
	key := httputil.GetAPIKey(r.Context())
	if key == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	sessionHash, _ := r.Context().Value(httputil.CtxKeySessionTokenHash).(string)
	identity := streamIdentity{name: key.Name, keyHash: key.Hash, sessionHash: sessionHash}
	ctx := context.WithValue(r.Context(), streamIdentityKey{}, identity)
	s.events.ServeHTTP(w, r.WithContext(ctx))
}

func (s *Server) authorizeEvent(r *http.Request, event *SSEEvent) (bool, error) {
	identity, ok := r.Context().Value(streamIdentityKey{}).(streamIdentity)
	if !ok || identity.name == "" || identity.keyHash == "" {
		return false, errStreamIdentity
	}
	key := s.cfg.LookupAPIKeyByName(identity.name)
	if key == nil || key.Hash != identity.keyHash {
		return false, errStreamIdentity
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if identity.sessionHash != "" {
		session, err := s.store.GetSessionByTokenHash(ctx, identity.sessionHash)
		if errors.Is(err, sql.ErrNoRows) {
			return false, errStreamIdentity
		}
		if err != nil {
			return false, err
		}
		if !time.Now().Before(session.ExpiresAt) || session.APIKeyName != identity.name || (session.KeyHash != "" && session.KeyHash != identity.keyHash) {
			return false, errStreamIdentity
		}
	}
	if event == nil {
		return key.HasCapability("monitors.read") || key.HasCapability("incidents.read") || key.HasCapability("sla.read"), nil
	}
	permission := event.permission()
	if permission == "" || !key.HasCapability(permission) {
		return false, nil
	}
	ctx = context.WithValue(ctx, httputil.CtxKeyAPIKey, key)
	return s.store.MonitorMatchesAccess(ctx, event.MonitorID, httputil.MonitorAccess(ctx, permission)...)
}

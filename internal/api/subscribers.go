package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/safenet"
	"github.com/y0f/asura/internal/storage"
)

const (
	maxSubscriberEmailLen   = 254
	maxSubscriberWebhookLen = 2048

	subscriberConfirmTimeout = time.Minute
)

// ListSubscribers returns the total subscriber count for a page. Keys that
// may manage subscribers (monitors.write) also get the confirmed subscribers,
// whose IDs can be passed to DeleteSubscriber. Read-only keys get only the
// count: emails are personal data and webhook URLs often embed tokens.
func (h *Handler) ListSubscribers(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid status page id")
		return
	}

	count, err := h.store.CountSubscribersByPage(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count subscribers")
		return
	}
	if key := httputil.GetAPIKey(r.Context()); key == nil || !key.HasPermission("monitors.write") {
		writeJSON(w, http.StatusOK, map[string]any{"count": count})
		return
	}
	subs, err := h.store.ListConfirmedSubscribers(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list subscribers")
		return
	}
	if subs == nil {
		subs = []*storage.StatusPageSubscriber{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"count": count, "data": subs})
}

// ConfirmSubscription serves the confirmation link from subscriber emails
// ({slug}/confirm?token=...) when the web UI, which normally serves it, is off.
func (h *Handler) ConfirmSubscription(w http.ResponseWriter, r *http.Request, pageID int64) {
	ctx := r.Context()
	token := r.URL.Query().Get("token")
	sub, err := h.store.GetSubscriberByToken(ctx, token)
	if token == "" || err != nil || sub.StatusPageID != pageID {
		writeError(w, http.StatusNotFound, "invalid or expired confirmation link")
		return
	}
	if !sub.Confirmed {
		if err := h.store.ConfirmSubscriber(ctx, token); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to confirm subscription")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "confirmed"})
}

// Unsubscribe serves the unsubscribe link from subscriber emails
// ({slug}/unsubscribe?token=...) when the web UI is off.
func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request, pageID int64) {
	ctx := r.Context()
	token := r.URL.Query().Get("token")
	sub, err := h.store.GetSubscriberByToken(ctx, token)
	if token == "" || err != nil || sub.StatusPageID != pageID {
		// Already removed or never existed: the outcome the caller wants.
		writeJSON(w, http.StatusOK, map[string]string{"status": "unsubscribed"})
		return
	}
	if err := h.store.DeleteSubscriberByToken(ctx, token); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to unsubscribe")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unsubscribed"})
}

func (h *Handler) DeleteSubscriber(w http.ResponseWriter, r *http.Request) {
	pageID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid status page id")
		return
	}
	subID, err := strconv.ParseInt(r.PathValue("subId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid subscriber id")
		return
	}

	// Scope the delete to the page in the URL so a subscriber cannot be removed
	// via another page's endpoint; the WHERE clause also covers unconfirmed rows.
	if err := h.store.DeleteSubscriber(r.Context(), pageID, subID); err != nil {
		writeError(w, http.StatusNotFound, "subscriber not found")
		return
	}

	h.audit(r, "delete", "subscriber", subID, fmt.Sprintf("deleted subscriber %d", subID))
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) SubscribeAPI(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid status page id")
		return
	}

	sp, err := h.store.GetStatusPage(r.Context(), id)
	if err != nil || sp == nil || !sp.Enabled {
		writeError(w, http.StatusNotFound, "status page not found")
		return
	}
	if !sp.APIEnabled {
		writeError(w, http.StatusNotFound, "status page not found")
		return
	}

	if !h.requireStatusPageAuth(w, r, sp) {
		return
	}

	var req struct {
		Type       string `json:"type"`
		Email      string `json:"email"`
		WebhookURL string `json:"webhook_url"`
	}
	if err := h.readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	req.Email = strings.TrimSpace(req.Email)
	req.WebhookURL = strings.TrimSpace(req.WebhookURL)

	switch req.Type {
	case "email":
		if msg := h.validateSubscriberEmail(req.Email); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	case "webhook":
		if msg := h.validateSubscriberWebhook(r.Context(), req.WebhookURL); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "type must be 'email' or 'webhook'")
		return
	}

	sub := &storage.StatusPageSubscriber{
		StatusPageID: sp.ID,
		Type:         req.Type,
		Email:        req.Email,
		WebhookURL:   req.WebhookURL,
		Confirmed:    req.Type == "webhook",
	}

	if err := h.store.CreateStatusPageSubscriber(r.Context(), sub); err != nil {
		writeError(w, http.StatusConflict, "subscription failed, may already exist")
		return
	}

	msg := "subscribed"
	if req.Type == "email" {
		// SubNotifier is nil when subscriptions are disabled; the subscriber
		// is stored all the same, as in v1.3.0.
		if h.SubNotifier != nil {
			h.sendConfirmation(r.Context(), sub, sp)
		}
		msg = "confirmation email sent"
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": msg})
}

// sendConfirmation emails the confirmation link in the background; the send
// must outlive the request, so it does not inherit its cancellation.
func (h *Handler) sendConfirmation(ctx context.Context, sub *storage.StatusPageSubscriber, sp *storage.StatusPage) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), subscriberConfirmTimeout)
	go func() {
		defer cancel()
		if err := h.SubNotifier.SendConfirmationEmail(ctx, sub, sp); err != nil {
			h.logger.Error("api: send confirmation email", "subscriber_id", sub.ID, "error", err)
		}
	}()
}

func (h *Handler) validateSubscriberEmail(email string) string {
	if email == "" {
		return "email is required"
	}
	if len(email) > maxSubscriberEmailLen {
		return "email is too long"
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return "invalid email address"
	}
	return ""
}

func (h *Handler) validateSubscriberWebhook(ctx context.Context, raw string) string {
	if raw == "" {
		return "webhook_url is required"
	}
	if len(raw) > maxSubscriberWebhookLen {
		return "webhook_url is too long"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "webhook_url must be a valid https URL"
	}
	if !h.cfg.Monitor.AllowPrivateTargets && !h.webhookHostAllowed(ctx, u.Hostname()) {
		return "webhook_url host is not allowed"
	}
	return ""
}

func (h *Handler) webhookHostAllowed(ctx context.Context, host string) bool {
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !safenet.IsPrivateIP(ip)
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if safenet.IsPrivateIP(ip.IP) {
			return false
		}
	}
	return true
}

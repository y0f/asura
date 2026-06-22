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

	"github.com/y0f/asura/internal/safenet"
	"github.com/y0f/asura/internal/storage"
)

const (
	maxSubscriberEmailLen   = 254
	maxSubscriberWebhookLen = 2048
)

func (h *Handler) CountSubscribers(w http.ResponseWriter, r *http.Request) {
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

	writeJSON(w, http.StatusOK, map[string]int64{"count": count})
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
		msg = "confirmation email sent"
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": msg})
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

package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/web/views"
)

func (h *Handler) StatusPageSubscribe(w http.ResponseWriter, r *http.Request, pageID int64) {
	if !h.cfg.Subscriptions.Enabled {
		http.Error(w, "Subscriptions are disabled", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	sp, err := h.store.GetStatusPage(ctx, pageID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Failed to load status page", http.StatusInternalServerError)
		return
	}
	if err != nil || sp == nil || !sp.Enabled {
		http.NotFound(w, r)
		return
	}
	if !h.requireStatusPageAuth(w, r, sp) {
		return
	}
	ip := httputil.ExtractIP(r, h.cfg.TrustedNets())
	if !h.loginRL.Allow(ip) {
		h.redirectToStatusPage(w, r, sp.Slug, "Too many requests. Try again later.")
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid subscription form", http.StatusBadRequest)
		return
	}
	input := httputil.SubscriptionInput{Type: r.PostForm.Get("type"), Email: r.PostForm.Get("email"), WebhookURL: r.PostForm.Get("webhook_url")}
	if msg := input.Validate(ctx, h.cfg.Monitor.AllowPrivateTargets); msg != "" {
		h.redirectToStatusPage(w, r, sp.Slug, msg)
		return
	}
	sub := &storage.StatusPageSubscriber{StatusPageID: sp.ID, Type: input.Type, Email: input.Email, WebhookURL: input.WebhookURL, Confirmed: input.Type == "webhook"}
	sp, err = h.store.CreatePublicStatusPageSubscriber(ctx, sub, storage.PublicStatusPageAccess{ExpectedPasswordHash: sp.PasswordHash, Audience: storage.PublicStatusPageWebSubscription})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.Error("web: create subscriber", "error", err)
		http.Error(w, "Failed to create subscription", http.StatusInternalServerError)
		return
	}
	if input.Type == "email" {
		err := fmt.Errorf("email subscriptions are unavailable")
		if h.subNotifier != nil {
			err = h.subNotifier.QueueConfirmationEmail(ctx, sub, sp)
		}
		if err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if cleanupErr := h.store.DeleteSubscriber(cleanupCtx, sp.ID, sub.ID); cleanupErr != nil {
				h.logger.Error("remove rejected subscription", "subscriber_id", sub.ID, "error", cleanupErr)
			}
			h.logger.Error("queue confirmation email", "subscriber_id", sub.ID, "error", err)
			h.redirectToStatusPage(w, r, sp.Slug, "Confirmation email unavailable. Try again later.")
			return
		}
		h.redirectToStatusPage(w, r, sp.Slug, "Check your email to confirm your subscription.")
		return
	}
	h.redirectToStatusPage(w, r, sp.Slug, "Subscribed successfully.")
}

func (h *Handler) StatusPageConfirm(w http.ResponseWriter, r *http.Request, pageID int64) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.NotFound(w, r)
		return
	}
	if err := h.store.ConfirmPageSubscriber(r.Context(), pageID, token); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.Error("web: confirm subscriber", "error", err)
		http.Error(w, "Failed to confirm subscription", http.StatusInternalServerError)
		return
	}
	h.subscriptionResult(w, r, "Subscription Confirmed", "Your subscription is active.")
}

func (h *Handler) StatusPageUnsubscribe(w http.ResponseWriter, r *http.Request, pageID int64) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.NotFound(w, r)
		return
	}
	if err := h.store.DeletePageSubscriberByToken(r.Context(), pageID, token); err != nil && !errors.Is(err, sql.ErrNoRows) {
		h.logger.Error("web: unsubscribe", "error", err)
		http.Error(w, "Failed to unsubscribe", http.StatusInternalServerError)
		return
	}
	h.subscriptionResult(w, r, "Unsubscribed", "You are no longer subscribed to updates.")
}

// Token results are generic: invalid or previously removed links must not
// disclose a page's current private branding or redirect the token elsewhere.
func (h *Handler) subscriptionResult(w http.ResponseWriter, r *http.Request, heading, message string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	h.renderComponent(w, r, views.SubscriberResultPage(views.SubscriberResultParams{Title: "Subscription", BasePath: h.cfg.Server.BasePath, Heading: heading, Message: message, Success: true}))
}

func (h *Handler) redirectToStatusPage(w http.ResponseWriter, r *http.Request, slug, msg string) {
	target := h.cfg.Server.BasePath + "/" + slug
	if msg != "" {
		target += "?msg=" + url.QueryEscape(msg)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

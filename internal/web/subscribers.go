package web

import (
	"net/http"
	"net/mail"
	"net/url"
	"strings"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/web/views"
)

func validateSubscription(r *http.Request) (subType, email, webhookURL, errMsg string) {
	subType = r.FormValue("type")
	email = strings.TrimSpace(r.FormValue("email"))
	webhookURL = strings.TrimSpace(r.FormValue("webhook_url"))

	switch subType {
	case "email":
		if email == "" {
			return subType, email, webhookURL, "Email address is required."
		}
		if _, err := mail.ParseAddress(email); err != nil {
			return subType, email, webhookURL, "Invalid email address."
		}
	case "webhook":
		if webhookURL == "" {
			return subType, email, webhookURL, "Webhook URL is required."
		}
		u, err := url.Parse(webhookURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return subType, email, webhookURL, "Invalid webhook URL."
		}
	default:
		return subType, email, webhookURL, "Invalid subscription type."
	}
	return subType, email, webhookURL, ""
}

func (h *Handler) StatusPageSubscribe(w http.ResponseWriter, r *http.Request, pageID int64) {
	ctx := r.Context()

	sp, err := h.store.GetStatusPage(ctx, pageID)
	if err != nil || sp == nil || !sp.Enabled {
		http.NotFound(w, r)
		return
	}

	ip := httputil.ExtractIP(r, h.cfg.TrustedNets())
	if !h.loginRL.Allow(ip) {
		h.redirectToStatusPage(w, r, sp.Slug, "Too many requests. Try again later.")
		return
	}

	subType, email, webhookURL, errMsg := validateSubscription(r)
	if errMsg != "" {
		h.redirectToStatusPage(w, r, sp.Slug, errMsg)
		return
	}

	sub := &storage.StatusPageSubscriber{
		StatusPageID: sp.ID,
		Type:         subType,
		Email:        email,
		WebhookURL:   webhookURL,
		Confirmed:    subType == "webhook",
	}

	if err := h.store.CreateStatusPageSubscriber(ctx, sub); err != nil {
		h.logger.Error("web: create subscriber", "error", err)
		h.redirectToStatusPage(w, r, sp.Slug, "Subscription failed. You may already be subscribed.")
		return
	}

	if subType == "email" && h.subNotifier != nil {
		go func() {
			if err := h.subNotifier.SendConfirmationEmail(ctx, sub, sp); err != nil {
				h.logger.Error("web: send confirmation email", "error", err)
			}
		}()
		h.redirectToStatusPage(w, r, sp.Slug, "Check your email to confirm your subscription.")
		return
	}

	h.redirectToStatusPage(w, r, sp.Slug, "Subscribed successfully.")
}

func (h *Handler) StatusPageConfirm(w http.ResponseWriter, r *http.Request, pageID int64) {
	ctx := r.Context()

	sp, err := h.store.GetStatusPage(ctx, pageID)
	if err != nil || sp == nil || !sp.Enabled {
		http.NotFound(w, r)
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" {
		http.NotFound(w, r)
		return
	}

	sub, err := h.store.GetSubscriberByToken(ctx, token)
	if err != nil || sub.StatusPageID != sp.ID {
		h.renderComponent(w, r, views.SubscriberResultPage(views.SubscriberResultParams{
			Title:    sp.Title,
			BasePath: h.cfg.Server.BasePath,
			Slug:     sp.Slug,
			Heading:  "Invalid Link",
			Message:  "This confirmation link is invalid or has expired.",
			Success:  false,
		}))
		return
	}

	if sub.Confirmed {
		h.renderComponent(w, r, views.SubscriberResultPage(views.SubscriberResultParams{
			Title:    sp.Title,
			BasePath: h.cfg.Server.BasePath,
			Slug:     sp.Slug,
			Heading:  "Already Confirmed",
			Message:  "Your subscription is already active.",
			Success:  true,
		}))
		return
	}

	if err := h.store.ConfirmSubscriber(ctx, token); err != nil {
		h.logger.Error("web: confirm subscriber", "error", err)
		h.renderComponent(w, r, views.SubscriberResultPage(views.SubscriberResultParams{
			Title:    sp.Title,
			BasePath: h.cfg.Server.BasePath,
			Slug:     sp.Slug,
			Heading:  "Error",
			Message:  "Could not confirm your subscription. Please try again.",
			Success:  false,
		}))
		return
	}

	h.renderComponent(w, r, views.SubscriberResultPage(views.SubscriberResultParams{
		Title:    sp.Title,
		BasePath: h.cfg.Server.BasePath,
		Slug:     sp.Slug,
		Heading:  "Subscription Confirmed",
		Message:  "You will receive status updates for " + sp.Title + ".",
		Success:  true,
	}))
}

func (h *Handler) StatusPageUnsubscribe(w http.ResponseWriter, r *http.Request, pageID int64) {
	ctx := r.Context()

	sp, err := h.store.GetStatusPage(ctx, pageID)
	if err != nil || sp == nil || !sp.Enabled {
		http.NotFound(w, r)
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" {
		http.NotFound(w, r)
		return
	}

	if err := h.store.DeleteSubscriberByToken(ctx, token); err != nil {
		h.renderComponent(w, r, views.SubscriberResultPage(views.SubscriberResultParams{
			Title:    sp.Title,
			BasePath: h.cfg.Server.BasePath,
			Slug:     sp.Slug,
			Heading:  "Already Unsubscribed",
			Message:  "You are no longer subscribed to updates.",
			Success:  true,
		}))
		return
	}

	h.renderComponent(w, r, views.SubscriberResultPage(views.SubscriberResultParams{
		Title:    sp.Title,
		BasePath: h.cfg.Server.BasePath,
		Slug:     sp.Slug,
		Heading:  "Unsubscribed",
		Message:  "You have been unsubscribed from " + sp.Title + ".",
		Success:  true,
	}))
}

func (h *Handler) redirectToStatusPage(w http.ResponseWriter, r *http.Request, slug, msg string) {
	target := h.cfg.Server.BasePath + "/" + slug
	if msg != "" {
		target += "?msg=" + url.QueryEscape(msg)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

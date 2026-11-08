package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
)

// ListSubscribers requires the explicit subscribers.read scope, because
// addresses and webhook URLs are not ordinary status-page metadata.
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
	subs, err := h.store.ListConfirmedSubscribers(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list subscribers")
		return
	}
	if subs == nil {
		subs = []*storage.StatusPageSubscriber{}
	}
	for i, sub := range subs {
		copy := *sub
		// Webhook URLs can contain bearer credentials in their path or query.
		copy.WebhookURL = ""
		subs[i] = &copy
	}

	writeJSON(w, http.StatusOK, map[string]any{"count": count, "data": subs})
}

// ConfirmSubscription serves the confirmation link from subscriber emails
// ({slug}/confirm?token=...) when the web UI, which normally serves it, is off.
func (h *Handler) ConfirmSubscription(w http.ResponseWriter, r *http.Request, pageID int64) {
	ctx := r.Context()
	token := r.URL.Query().Get("token")
	if token == "" {
		writeError(w, http.StatusNotFound, "invalid or expired confirmation link")
		return
	}
	if err := h.store.ConfirmPageSubscriber(ctx, pageID, token); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "invalid or expired confirmation link")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to confirm subscription")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "confirmed"})
}

// Unsubscribe serves the unsubscribe link from subscriber emails
// ({slug}/unsubscribe?token=...) when the web UI is off.
func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request, pageID int64) {
	ctx := r.Context()
	token := r.URL.Query().Get("token")
	if token == "" {
		// Already removed or never existed: the outcome the caller wants.
		writeJSON(w, http.StatusOK, map[string]string{"status": "unsubscribed"})
		return
	}
	if err := h.store.DeletePageSubscriberByToken(ctx, pageID, token); err != nil && !errors.Is(err, sql.ErrNoRows) {
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
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "subscriber not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete subscriber")
		return
	}

	h.audit(r, "delete", "subscriber", subID, fmt.Sprintf("deleted subscriber %d", subID))
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) SubscribeAPI(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.Subscriptions.Enabled {
		writeError(w, http.StatusServiceUnavailable, "subscriptions are disabled")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid status page id")
		return
	}

	sp, err := h.store.GetStatusPage(r.Context(), id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load status page")
		return
	}
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

	var req httputil.SubscriptionInput
	if err := h.readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if msg := req.Validate(r.Context(), h.cfg.Monitor.AllowPrivateTargets); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	sub := &storage.StatusPageSubscriber{
		StatusPageID: sp.ID,
		Type:         req.Type,
		Email:        req.Email,
		WebhookURL:   req.WebhookURL,
		Confirmed:    req.Type == "webhook",
	}

	sp, err = h.store.CreatePublicStatusPageSubscriber(r.Context(), sub, storage.PublicStatusPageAccess{
		ExpectedPasswordHash: sp.PasswordHash, Audience: storage.PublicStatusPageAPISubscription,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "status page not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create subscription")
		return
	}

	msg := "subscribed"
	if req.Type == "email" {
		err := fmt.Errorf("email subscriptions are unavailable")
		if h.SubNotifier != nil {
			err = h.SubNotifier.QueueConfirmationEmail(r.Context(), sub, sp)
		}
		if err != nil {
			// Remove only the row created by this request, so a queue rejection
			// does not leave an unconfirmable subscription that blocks retry.
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if cleanupErr := h.store.DeleteSubscriber(cleanupCtx, sp.ID, sub.ID); cleanupErr != nil {
				h.logger.Error("remove rejected subscription", "subscriber_id", sub.ID, "error", cleanupErr)
			}
			h.logger.Error("queue confirmation email", "subscriber_id", sub.ID, "error", err)
			writeError(w, http.StatusServiceUnavailable, "confirmation email unavailable; try again later")
			return
		}
		msg = "confirmation email queued"
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": msg})
}

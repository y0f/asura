package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/y0f/asura/internal/storage"
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
	subID, err := strconv.ParseInt(r.PathValue("subId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid subscriber id")
		return
	}

	if err := h.store.DeleteSubscriber(r.Context(), subID); err != nil {
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

	var req struct {
		Type       string `json:"type"`
		Email      string `json:"email"`
		WebhookURL string `json:"webhook_url"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Type != "email" && req.Type != "webhook" {
		writeError(w, http.StatusBadRequest, "type must be 'email' or 'webhook'")
		return
	}

	if req.Type == "email" && req.Email == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}
	if req.Type == "webhook" && req.WebhookURL == "" {
		writeError(w, http.StatusBadRequest, "webhook_url is required")
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

	status := http.StatusCreated
	msg := "subscribed"
	if req.Type == "email" {
		msg = "confirmation email sent"
	}
	writeJSON(w, status, map[string]string{"status": msg})
}

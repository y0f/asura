package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/incident"
	"github.com/y0f/asura/internal/redact"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
)

func (h *Handler) ListNotifications(w http.ResponseWriter, r *http.Request) {
	channels, err := h.store.ListNotificationChannels(r.Context())
	if err != nil {
		h.logger.Error("list notifications", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list notification channels")
		return
	}
	for i, channel := range channels {
		channels[i] = redact.Notification(channel)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": channels})
}

func (h *Handler) CreateNotification(w http.ResponseWriter, r *http.Request) {
	type channelFields storage.NotificationChannel
	var req struct {
		channelFields
		Enabled *bool `json:"enabled"`
	}
	if err := h.readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ch := storage.NotificationChannel(req.channelFields)
	ch.Enabled = true
	if req.Enabled != nil {
		ch.Enabled = *req.Enabled
	}

	if err := validate.ValidateNotificationChannel(&ch); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.CreateNotificationChannel(r.Context(), &ch); err != nil {
		h.logger.Error("create notification", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create notification channel")
		return
	}

	h.audit(r, "create", "notification_channel", ch.ID, "")
	writeJSON(w, http.StatusCreated, notificationResponse(r, &ch))
}

func (h *Handler) UpdateNotification(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	existing, err := h.store.GetNotificationChannel(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "notification channel not found")
			return
		}
		h.logger.Error("get notification for update", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get notification channel")
		return
	}

	type channelFields storage.NotificationChannel
	var req struct {
		channelFields
		ClearSecrets []string `json:"clear_secrets"`
	}
	if err := h.readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ch := storage.NotificationChannel(req.channelFields)
	ch.ID = id
	if ch.Type == existing.Type {
		ch.Settings, err = redact.MergeSecrets(ch.Settings, existing.Settings, redact.NotificationSecretKeys[ch.Type], secretClearSet(req.ClearSecrets))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if err := validate.ValidateNotificationChannel(&ch); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.UpdateNotificationChannel(r.Context(), &ch); err != nil {
		h.logger.Error("update notification", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update notification channel")
		return
	}

	h.audit(r, "update", "notification_channel", ch.ID, "")
	writeJSON(w, http.StatusOK, notificationResponse(r, &ch))
}

func (h *Handler) DeleteNotification(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	_, err = h.store.GetNotificationChannel(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "notification channel not found")
			return
		}
		h.logger.Error("get notification for delete", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get notification channel")
		return
	}

	if err := h.store.DeleteNotificationChannel(r.Context(), id); err != nil {
		h.logger.Error("delete notification", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete notification channel")
		return
	}

	h.audit(r, "delete", "notification_channel", id, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) TestNotification(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ch, err := h.store.GetNotificationChannel(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "notification channel not found")
			return
		}
		h.logger.Error("get notification for test", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get notification channel")
		return
	}

	if h.notifier == nil {
		writeError(w, http.StatusServiceUnavailable, "notification system not available")
		return
	}

	testIncident := &storage.Incident{
		ID:          0,
		MonitorID:   0,
		MonitorName: "Test Monitor",
		Status:      incident.StatusOpen,
		Cause:       "This is a test notification",
	}

	if err := h.notifier.SendTest(r.Context(), ch, testIncident); err != nil {
		h.logger.Error("notification test failed", "channel_id", ch.ID, "error", err)
		writeError(w, http.StatusBadGateway, "test notification failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

func (h *Handler) ListNotificationHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := storage.NotifHistoryFilter{Access: httputil.MonitorAccess(r.Context(), "notification_history.read")}
	if v := q.Get("channel_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.ChannelID = id
		}
	}
	f.Status = q.Get("status")
	f.EventType = q.Get("event_type")

	p := httputil.ParsePagination(r)
	result, err := h.store.ListNotificationHistory(r.Context(), f, p)
	if err != nil {
		h.logger.Error("list notification history", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list notification history")
		return
	}
	writeJSON(w, http.StatusOK, httputil.NotificationHistoryResponse(r.Context(), result))
}

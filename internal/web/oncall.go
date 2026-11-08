package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/y0f/asura/v2/internal/httputil"
	"github.com/y0f/asura/v2/internal/storage"
	"github.com/y0f/asura/v2/internal/web/views"
)

func (h *Handler) OnCallRotations(w http.ResponseWriter, r *http.Request) {
	rotations, err := h.store.ListOnCallRotations(r.Context())
	if err != nil {
		h.logger.Error("web: list on-call rotations", "error", err)
		http.Error(w, "Failed to load on-call rotations", http.StatusInternalServerError)
		return
	}
	var channels []*storage.NotificationChannel
	if httputil.GetAPIKey(r.Context()).HasPermission("notifications.read") {
		channels, err = h.store.ListNotificationChannels(r.Context())
		if err != nil {
			h.logger.Error("web: list on-call channels", "error", err)
			http.Error(w, "Failed to load on-call channels", http.StatusInternalServerError)
			return
		}
	}
	lp := h.newLayoutParams(r, "On-Call", "on-call")
	h.renderComponent(w, r, views.OnCallListPage(views.OnCallListParams{
		LayoutParams: lp,
		Rotations:    rotations,
		Channels:     channels,
	}))
}

func (h *Handler) OnCallCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid on-call form", http.StatusBadRequest)
		return
	}
	if !httputil.GetAPIKey(r.Context()).HasPermission("notifications.read") {
		http.Error(w, "Notification read access is required to assign channels", http.StatusForbidden)
		return
	}
	name := r.FormValue("name")
	period := r.FormValue("period")
	if name == "" {
		h.setError(w, "Name is required")
		h.redirectToReadable(w, r, "/on-call")
		return
	}
	if period != "daily" && period != "weekly" {
		h.setError(w, "Period must be daily or weekly")
		h.redirectToReadable(w, r, "/on-call")
		return
	}
	var channelIDs []int64
	for _, v := range r.Form["channel_ids[]"] {
		id, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || id <= 0 {
			h.setError(w, "Select valid notification channels")
			h.redirectToReadable(w, r, "/on-call")
			return
		}
		channelIDs = append(channelIDs, id)
	}
	if len(channelIDs) == 0 {
		h.setError(w, "Select at least one channel")
		h.redirectToReadable(w, r, "/on-call")
		return
	}
	rot := &storage.OnCallRotation{
		Name:       name,
		ChannelIDs: channelIDs,
		Period:     period,
	}
	if err := h.store.CreateOnCallRotation(r.Context(), rot); err != nil {
		h.logger.Error("web: create on-call rotation", "error", err)
		if errors.Is(err, storage.ErrInvalidRotation) {
			h.setError(w, err.Error())
		} else {
			h.setError(w, "Failed to create rotation")
		}
		h.redirectToReadable(w, r, "/on-call")
		return
	}
	h.audit(r, "create", "on_call_rotation", rot.ID, rot.Name)
	h.setFlash(w, "Rotation created")
	h.redirectToReadable(w, r, "/on-call")
}

func (h *Handler) OnCallDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirectToReadable(w, r, "/on-call")
		return
	}
	if err := h.store.DeleteOnCallRotation(r.Context(), id); err != nil {
		h.logger.Error("web: delete on-call rotation", "error", err)
		h.setError(w, "Failed to delete rotation")
		h.redirectToReadable(w, r, "/on-call")
		return
	}
	h.audit(r, "delete", "on_call_rotation", id, "")
	h.setFlash(w, "Rotation deleted")
	h.redirectToReadable(w, r, "/on-call")
}

func (h *Handler) OnCallOverride(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid on-call form", http.StatusBadRequest)
		return
	}
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirectToReadable(w, r, "/on-call")
		return
	}
	channelValue, expiryValue := r.FormValue("override_channel_id"), r.FormValue("override_until")
	var channelID *int64
	var expiry *time.Time
	if channelValue != "" {
		chID, idErr := strconv.ParseInt(channelValue, 10, 64)
		until, timeErr := time.Parse("2006-01-02T15:04", expiryValue)
		if idErr != nil || chID <= 0 || timeErr != nil || !until.After(time.Now()) {
			h.setError(w, "Select a channel and a future override expiry in UTC")
			h.redirectToReadable(w, r, "/on-call")
			return
		}
		if !httputil.GetAPIKey(r.Context()).HasPermission("notifications.read") {
			http.Error(w, "Notification read access is required to assign channels", http.StatusForbidden)
			return
		}
		channelID, expiry = &chID, &until
	}
	_, err = h.store.UpdateOnCallRotation(r.Context(), id, func(rot *storage.OnCallRotation) error {
		rot.OverrideChannelID, rot.OverrideUntil = channelID, expiry
		return nil
	})
	if err != nil {
		h.logger.Error("web: override on-call", "error", err)
		if errors.Is(err, storage.ErrInvalidRotation) {
			h.setError(w, err.Error())
		} else {
			h.setError(w, "Failed to set override")
		}
	} else {
		h.audit(r, "override", "on_call_rotation", id, "")
		if channelID != nil {
			h.setFlash(w, "Override set")
		} else {
			h.setFlash(w, "Override cleared")
		}
	}
	h.redirectToReadable(w, r, "/on-call")
}

package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/web/views"
)

func (h *Handler) OnCallRotations(w http.ResponseWriter, r *http.Request) {
	rotations, err := h.store.ListOnCallRotations(r.Context())
	if err != nil {
		h.logger.Error("web: list on-call rotations", "error", err)
	}
	channels, _ := h.store.ListNotificationChannels(r.Context())
	lp := h.newLayoutParams(r, "On-Call", "on-call")
	h.renderComponent(w, r, views.OnCallListPage(views.OnCallListParams{
		LayoutParams: lp,
		Rotations:    rotations,
		Channels:     channels,
	}))
}

func (h *Handler) OnCallCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	period := r.FormValue("period")
	if name == "" {
		h.setFlash(w, "Name is required")
		h.redirect(w, r, "/on-call")
		return
	}
	if period != "daily" && period != "weekly" {
		period = "daily"
	}
	var channelIDs []int64
	for _, v := range r.Form["channel_ids[]"] {
		if id, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			channelIDs = append(channelIDs, id)
		}
	}
	if len(channelIDs) == 0 {
		h.setFlash(w, "Select at least one channel")
		h.redirect(w, r, "/on-call")
		return
	}
	rot := &storage.OnCallRotation{
		Name:       name,
		ChannelIDs: channelIDs,
		Period:     period,
	}
	if err := h.store.CreateOnCallRotation(r.Context(), rot); err != nil {
		h.logger.Error("web: create on-call rotation", "error", err)
		h.setFlash(w, "Failed to create rotation")
		h.redirect(w, r, "/on-call")
		return
	}
	h.audit(r, "create", "on_call_rotation", rot.ID, rot.Name)
	h.setFlash(w, "Rotation created")
	h.redirect(w, r, "/on-call")
}

func (h *Handler) OnCallDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/on-call")
		return
	}
	if err := h.store.DeleteOnCallRotation(r.Context(), id); err != nil {
		h.logger.Error("web: delete on-call rotation", "error", err)
	}
	h.setFlash(w, "Rotation deleted")
	h.redirect(w, r, "/on-call")
}

func (h *Handler) OnCallOverride(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/on-call")
		return
	}
	rot, err := h.store.GetOnCallRotation(r.Context(), id)
	if err != nil {
		h.setFlash(w, "Rotation not found")
		h.redirect(w, r, "/on-call")
		return
	}
	chID, _ := strconv.ParseInt(r.FormValue("override_channel_id"), 10, 64)
	until, _ := time.Parse("2006-01-02T15:04", r.FormValue("override_until"))

	if chID > 0 && !until.IsZero() {
		rot.OverrideChannelID = &chID
		rot.OverrideUntil = &until
	} else {
		rot.OverrideChannelID = nil
		rot.OverrideUntil = nil
	}

	if err := h.store.UpdateOnCallRotation(r.Context(), rot); err != nil {
		h.logger.Error("web: override on-call", "error", err)
		h.setFlash(w, "Failed to set override")
	} else if chID > 0 {
		h.setFlash(w, "Override set")
	} else {
		h.setFlash(w, "Override cleared")
	}
	h.redirect(w, r, "/on-call")
}

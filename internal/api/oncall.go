package api

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
)

func (h *Handler) ListOnCallRotations(w http.ResponseWriter, r *http.Request) {
	rotations, err := h.store.ListOnCallRotations(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list rotations")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rotations})
}

func (h *Handler) CreateOnCallRotation(w http.ResponseWriter, r *http.Request) {
	var rot storage.OnCallRotation
	if err := readJSON(r, &rot); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if rot.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(rot.ChannelIDs) == 0 {
		writeError(w, http.StatusBadRequest, "at least one channel is required")
		return
	}
	if rot.Period != "daily" && rot.Period != "weekly" {
		writeError(w, http.StatusBadRequest, "period must be daily or weekly")
		return
	}
	if err := h.store.CreateOnCallRotation(r.Context(), &rot); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create rotation")
		return
	}
	h.audit(r, "create", "on_call_rotation", rot.ID, rot.Name)
	writeJSON(w, http.StatusCreated, rot)
}

func (h *Handler) UpdateOnCallRotation(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := h.store.GetOnCallRotation(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "rotation not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get rotation")
		return
	}
	var rot storage.OnCallRotation
	if err := readJSON(r, &rot); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rot.ID = id
	if err := h.store.UpdateOnCallRotation(r.Context(), &rot); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update rotation")
		return
	}
	h.audit(r, "update", "on_call_rotation", id, rot.Name)
	writeJSON(w, http.StatusOK, rot)
}

func (h *Handler) DeleteOnCallRotation(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.DeleteOnCallRotation(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete rotation")
		return
	}
	h.audit(r, "delete", "on_call_rotation", id, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) GetCurrentOnCall(w http.ResponseWriter, r *http.Request) {
	rotations, err := h.store.ListOnCallRotations(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list rotations")
		return
	}
	channels, _ := h.store.ListNotificationChannels(r.Context())
	channelMap := make(map[int64]string)
	for _, ch := range channels {
		channelMap[ch.ID] = ch.Name
	}

	type onCallInfo struct {
		RotationID   int64  `json:"rotation_id"`
		RotationName string `json:"rotation_name"`
		ChannelID    int64  `json:"channel_id"`
		ChannelName  string `json:"channel_name"`
		Override     bool   `json:"override"`
	}

	var current []onCallInfo
	for _, rot := range rotations {
		chID := rot.CurrentOnCallChannelID()
		if chID == 0 {
			continue
		}
		isOverride := rot.OverrideChannelID != nil && rot.OverrideUntil != nil
		current = append(current, onCallInfo{
			RotationID:   rot.ID,
			RotationName: rot.Name,
			ChannelID:    chID,
			ChannelName:  channelMap[chID],
			Override:     isOverride,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"on_call": current})
}

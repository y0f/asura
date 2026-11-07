package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

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
	key := httputil.GetAPIKey(r.Context())
	if !key.HasPermission("notifications.read") {
		writeError(w, http.StatusForbidden, "notification read access is required to assign channels")
		return
	}
	var rot storage.OnCallRotation
	if err := h.readJSON(r, &rot); err != nil {
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
	if (rot.OverrideChannelID != nil || rot.OverrideUntil != nil) && !key.HasPermission("on_call.override") {
		writeError(w, http.StatusForbidden, "on-call override permission is required")
		return
	}
	if rot.OverrideUntil != nil && !rot.OverrideUntil.After(time.Now()) {
		writeError(w, http.StatusBadRequest, "override expiry must be in the future")
		return
	}
	if err := h.store.CreateOnCallRotation(r.Context(), &rot); err != nil {
		writeRotationError(w, err)
		return
	}
	h.audit(r, "create", "on_call_rotation", rot.ID, rot.Name)
	writeJSON(w, http.StatusCreated, rotationResponse(r, &rot))
}

func (h *Handler) UpdateOnCallRotation(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body json.RawMessage
	if err := h.readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	key := httputil.GetAPIKey(r.Context())
	rot, err := h.store.UpdateOnCallRotation(r.Context(), id, func(rot *storage.OnCallRotation) error {
		channels := slices.Clone(rot.ChannelIDs)
		oldID, oldUntil := rotationOverrideValues(rot)
		body = bytes.TrimSpace(body)
		if len(body) == 0 || body[0] != '{' {
			return storage.ErrInvalidRotation
		}
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(rot); err != nil {
			return storage.ErrInvalidRotation
		}
		newID, newUntil := rotationOverrideValues(rot)
		if !slices.Equal(channels, rot.ChannelIDs) && !key.HasPermission("notifications.read") {
			return errRotationPermission
		}
		if oldID != newID || !oldUntil.Equal(newUntil) {
			if !key.HasPermission("on_call.override") || (rot.OverrideChannelID != nil && !key.HasPermission("notifications.read")) {
				return errRotationPermission
			}
			if rot.OverrideUntil != nil && !rot.OverrideUntil.After(time.Now()) {
				return storage.ErrInvalidRotation
			}
		}
		return nil
	})
	if err != nil {
		writeRotationError(w, err)
		return
	}
	h.audit(r, "update", "on_call_rotation", id, rot.Name)
	writeJSON(w, http.StatusOK, rotationResponse(r, rot))
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
	channelMap := make(map[int64]string)
	if httputil.GetAPIKey(r.Context()).HasPermission("notifications.read") {
		channels, err := h.store.ListNotificationChannels(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list channels")
			return
		}
		for _, ch := range channels {
			channelMap[ch.ID] = ch.Name
		}
	}

	type onCallInfo struct {
		RotationID   int64  `json:"rotation_id"`
		RotationName string `json:"rotation_name"`
		ChannelID    int64  `json:"channel_id"`
		ChannelName  string `json:"channel_name,omitempty"`
		Override     bool   `json:"override"`
	}

	current := []onCallInfo{}
	for _, rot := range rotations {
		chID := rot.CurrentOnCallChannelID()
		if chID == 0 {
			continue
		}
		isOverride := rot.OverrideActive(time.Now())
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

var errRotationPermission = errors.New("on-call assignment or override permission is required")

func rotationOverrideValues(rot *storage.OnCallRotation) (id int64, until time.Time) {
	if rot.OverrideChannelID != nil {
		id = *rot.OverrideChannelID
	}
	if rot.OverrideUntil != nil {
		until = *rot.OverrideUntil
	}
	return
}

func writeRotationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "rotation not found")
	case errors.Is(err, errRotationPermission):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, storage.ErrInvalidRotation):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "failed to save rotation")
	}
}

func rotationResponse(r *http.Request, rot *storage.OnCallRotation) any {
	if !httputil.GetAPIKey(r.Context()).HasPermission("on_call.read") {
		return map[string]int64{"id": rot.ID}
	}
	return rot
}

func (h *Handler) OnCallOverride(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var input struct {
		ChannelID *int64     `json:"override_channel_id"`
		Until     *time.Time `json:"override_until"`
	}
	if err := h.readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.ChannelID != nil && !httputil.GetAPIKey(r.Context()).HasPermission("notifications.read") {
		writeRotationError(w, errRotationPermission)
		return
	}
	if input.Until != nil && !input.Until.After(time.Now()) {
		writeError(w, http.StatusBadRequest, "override expiry must be in the future")
		return
	}
	rot, err := h.store.UpdateOnCallRotation(r.Context(), id, func(rot *storage.OnCallRotation) error {
		rot.OverrideChannelID, rot.OverrideUntil = input.ChannelID, input.Until
		return nil
	})
	if err != nil {
		writeRotationError(w, err)
		return
	}
	h.audit(r, "override", "on_call_rotation", id, "")
	writeJSON(w, http.StatusOK, rotationResponse(r, rot))
}

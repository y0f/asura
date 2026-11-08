package api

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
)

func (h *Handler) ListEscalationPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := h.store.ListEscalationPolicies(r.Context())
	if err != nil {
		h.logger.Error("list escalation policies", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list escalation policies")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": policies})
}

func (h *Handler) GetEscalationPolicy(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ep, err := h.store.GetEscalationPolicy(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "escalation policy not found")
			return
		}
		h.logger.Error("get escalation policy", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get escalation policy")
		return
	}

	writeJSON(w, http.StatusOK, ep)
}

func (h *Handler) CreateEscalationPolicy(w http.ResponseWriter, r *http.Request) {
	h.saveEscalationPolicy(w, r, 0)
}

func (h *Handler) UpdateEscalationPolicy(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.saveEscalationPolicy(w, r, id)
}

func (h *Handler) saveEscalationPolicy(w http.ResponseWriter, r *http.Request, id int64) {
	var input httputil.EscalationPolicyInput
	if err := h.readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	key := httputil.GetAPIKey(r.Context())
	saved, err := h.store.SaveEscalationPolicy(r.Context(), id, func(current *storage.EscalationPolicy) (*storage.EscalationPolicy, error) {
		return input.Apply(current, key.HasPermission("notifications.read"))
	})
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeError(w, http.StatusNotFound, "escalation policy not found")
		case errors.Is(err, httputil.ErrEscalationChannelPermission):
			writeError(w, http.StatusForbidden, err.Error())
		case errors.Is(err, httputil.ErrInvalidEscalationPolicy), errors.Is(err, storage.ErrInvalidEscalationChannels):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			h.logger.Error("save escalation policy", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to save escalation policy")
		}
		return
	}
	status, action := http.StatusOK, "update"
	if id == 0 {
		status, action = http.StatusCreated, "create"
	}
	h.audit(r, action, "escalation_policy", saved.ID, "")
	if !key.HasPermission("escalation_policies.read") {
		writeJSON(w, status, map[string]int64{"id": saved.ID})
		return
	}
	writeJSON(w, status, saved)
}

func (h *Handler) DeleteEscalationPolicy(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.DeleteEscalationPolicy(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "escalation policy not found")
			return
		}
		h.logger.Error("delete escalation policy", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete escalation policy")
		return
	}
	h.audit(r, "delete", "escalation_policy", id, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

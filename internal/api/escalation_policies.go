package api

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
)

func (h *Handler) ListEscalationPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := h.store.ListEscalationPolicies(r.Context())
	if err != nil {
		h.logger.Error("list escalation policies", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list escalation policies")
		return
	}
	for _, ep := range policies {
		steps, err := h.store.GetEscalationPolicySteps(r.Context(), ep.ID)
		if err != nil {
			h.logger.Error("get escalation steps", "error", err, "policy_id", ep.ID)
			continue
		}
		ep.Steps = steps
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
	ep.Steps, _ = h.store.GetEscalationPolicySteps(r.Context(), ep.ID)
	writeJSON(w, http.StatusOK, ep)
}

func (h *Handler) CreateEscalationPolicy(w http.ResponseWriter, r *http.Request) {
	var ep storage.EscalationPolicy
	if err := readJSON(r, &ep); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validate.ValidateEscalationPolicy(&ep); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.CreateEscalationPolicy(r.Context(), &ep); err != nil {
		h.logger.Error("create escalation policy", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create escalation policy")
		return
	}
	if len(ep.Steps) > 0 {
		for i := range ep.Steps {
			ep.Steps[i].StepOrder = i
		}
		if err := h.store.ReplaceEscalationPolicySteps(r.Context(), ep.ID, ep.Steps); err != nil {
			h.logger.Error("create escalation steps", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to create escalation steps")
			return
		}
	}
	ep.Steps, _ = h.store.GetEscalationPolicySteps(r.Context(), ep.ID)
	h.audit(r, "create", "escalation_policy", ep.ID, "")
	writeJSON(w, http.StatusCreated, ep)
}

func (h *Handler) UpdateEscalationPolicy(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := h.store.GetEscalationPolicy(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "escalation policy not found")
			return
		}
		h.logger.Error("get escalation policy for update", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get escalation policy")
		return
	}
	var ep storage.EscalationPolicy
	if err := readJSON(r, &ep); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ep.ID = id
	if err := validate.ValidateEscalationPolicy(&ep); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.UpdateEscalationPolicy(r.Context(), &ep); err != nil {
		h.logger.Error("update escalation policy", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update escalation policy")
		return
	}
	for i := range ep.Steps {
		ep.Steps[i].StepOrder = i
	}
	if err := h.store.ReplaceEscalationPolicySteps(r.Context(), ep.ID, ep.Steps); err != nil {
		h.logger.Error("replace escalation steps", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update escalation steps")
		return
	}
	ep.Steps, _ = h.store.GetEscalationPolicySteps(r.Context(), ep.ID)
	h.audit(r, "update", "escalation_policy", ep.ID, "")
	writeJSON(w, http.StatusOK, ep)
}

func (h *Handler) DeleteEscalationPolicy(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := h.store.GetEscalationPolicy(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "escalation policy not found")
			return
		}
		h.logger.Error("get escalation policy for delete", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get escalation policy")
		return
	}
	if err := h.store.DeleteEscalationPolicy(r.Context(), id); err != nil {
		h.logger.Error("delete escalation policy", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete escalation policy")
		return
	}
	h.audit(r, "delete", "escalation_policy", id, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

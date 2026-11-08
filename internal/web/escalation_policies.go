package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/y0f/asura/v2/internal/httputil"
	"github.com/y0f/asura/v2/internal/storage"
	"github.com/y0f/asura/v2/internal/web/views"
)

func (h *Handler) EscalationPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := h.store.ListEscalationPolicies(r.Context())
	if err != nil {
		h.logger.Error("web: list escalation policies", "error", err)
		http.Error(w, "Failed to load escalation policies", http.StatusInternalServerError)
		return
	}

	var channels []*storage.NotificationChannel
	if httputil.GetAPIKey(r.Context()).HasPermission("notifications.read") {
		channels, err = h.store.ListNotificationChannels(r.Context())
		if err != nil {
			h.logger.Error("web: escalation channel choices", "error", err)
			http.Error(w, "Failed to load notification channels", http.StatusInternalServerError)
			return
		}
	}

	lp := h.newLayoutParams(r, "Escalation Policies", "escalation-policies")
	h.renderComponent(w, r, views.EscalationPolicyListPage(views.EscalationPolicyListParams{
		LayoutParams: lp,
		Policies:     policies,
		Channels:     channels,
	}))
}

func (h *Handler) EscalationPolicyCreate(w http.ResponseWriter, r *http.Request) {
	h.saveEscalationPolicy(w, r, 0)
}

func (h *Handler) EscalationPolicyUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		http.Error(w, "Invalid policy ID", http.StatusBadRequest)
		return
	}
	h.saveEscalationPolicy(w, r, id)
}

func (h *Handler) saveEscalationPolicy(w http.ResponseWriter, r *http.Request, id int64) {
	input, err := parseEscalationPolicyForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	key := httputil.GetAPIKey(r.Context())
	saved, err := h.store.SaveEscalationPolicy(r.Context(), id, func(current *storage.EscalationPolicy) (*storage.EscalationPolicy, error) {
		return input.Apply(current, key.HasPermission("notifications.read"))
	})
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			http.NotFound(w, r)
		case errors.Is(err, httputil.ErrEscalationChannelPermission):
			http.Error(w, err.Error(), http.StatusForbidden)
		case errors.Is(err, httputil.ErrInvalidEscalationPolicy), errors.Is(err, storage.ErrInvalidEscalationChannels):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			h.logger.Error("web: save escalation policy", "error", err)
			http.Error(w, "Failed to save escalation policy", http.StatusInternalServerError)
		}
		return
	}
	if id == 0 {
		h.audit(r, "create", "escalation_policy", saved.ID, "")
		h.setFlash(w, "Escalation policy created")
	} else {
		h.audit(r, "update", "escalation_policy", saved.ID, "")
		h.setFlash(w, "Escalation policy updated")
	}
	h.escalationRedirect(w, r)
}

func (h *Handler) EscalationPolicyDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		http.Error(w, "Invalid policy ID", http.StatusBadRequest)
		return
	}
	if err := h.store.DeleteEscalationPolicy(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.Error("web: delete escalation policy", "error", err)
		http.Error(w, "Failed to delete escalation policy", http.StatusInternalServerError)
		return
	}
	h.audit(r, "delete", "escalation_policy", id, "")
	h.setFlash(w, "Escalation policy deleted")
	h.escalationRedirect(w, r)
}

func (h *Handler) escalationRedirect(w http.ResponseWriter, r *http.Request) {
	lp := h.newLayoutParams(r, "", "")
	if lp.Can("escalation_policies.read") {
		h.redirect(w, r, "/escalation-policies")
		return
	}
	lp.BasePath = ""
	h.redirect(w, r, lp.HomePath())
}

func parseEscalationPolicyForm(r *http.Request) (httputil.EscalationPolicyInput, error) {
	var input httputil.EscalationPolicyInput
	if err := r.ParseForm(); err != nil {
		return input, fmt.Errorf("invalid form")
	}
	name, description := r.FormValue("name"), r.FormValue("description")
	enabled, repeat := r.FormValue("enabled") == "on", r.FormValue("repeat") == "on"
	input.Name, input.Description, input.Enabled, input.Repeat = &name, &description, &enabled, &repeat
	delays, channels := r.Form["step_delay_minutes[]"], r.Form["step_channels[]"]
	if r.Form.Has("steps_present") || r.Form.Has("step_delay_minutes[]") || r.Form.Has("step_channels[]") {
		if len(delays) != len(channels) {
			return input, fmt.Errorf("each escalation step must include a delay and channel selection")
		}
		steps := make([]*storage.EscalationPolicyStep, len(delays))
		for i, value := range delays {
			delay, err := strconv.Atoi(value)
			if err != nil {
				return input, fmt.Errorf("step %d has an invalid delay", i+1)
			}
			var ids []int64
			if err := json.Unmarshal([]byte(channels[i]), &ids); err != nil {
				return input, fmt.Errorf("step %d has invalid channel IDs", i+1)
			}
			steps[i] = &storage.EscalationPolicyStep{DelayMinutes: delay, NotificationChannelIDs: ids}
		}
		input.Steps = &steps
	}
	return input, nil
}

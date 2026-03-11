package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
	"github.com/y0f/asura/internal/web/views"
)

func (h *Handler) EscalationPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := h.store.ListEscalationPolicies(r.Context())
	if err != nil {
		h.logger.Error("web: list escalation policies", "error", err)
	}
	for _, ep := range policies {
		steps, err := h.store.GetEscalationPolicySteps(r.Context(), ep.ID)
		if err != nil {
			h.logger.Error("web: get escalation steps", "error", err, "policy_id", ep.ID)
			continue
		}
		ep.Steps = steps
	}
	channels, _ := h.store.ListNotificationChannels(r.Context())

	lp := h.newLayoutParams(r, "Escalation Policies", "escalation-policies")
	h.renderComponent(w, r, views.EscalationPolicyListPage(views.EscalationPolicyListParams{
		LayoutParams: lp,
		Policies:     policies,
		Channels:     channels,
	}))
}

func (h *Handler) EscalationPolicyCreate(w http.ResponseWriter, r *http.Request) {
	ep, steps := h.parseEscalationPolicyForm(r)

	if err := validate.ValidateEscalationPolicy(ep); err != nil {
		h.setFlash(w, err.Error())
		h.redirect(w, r, "/escalation-policies")
		return
	}

	if err := h.store.CreateEscalationPolicy(r.Context(), ep); err != nil {
		h.logger.Error("web: create escalation policy", "error", err)
		h.setFlash(w, "Failed to create escalation policy")
		h.redirect(w, r, "/escalation-policies")
		return
	}

	if err := h.store.ReplaceEscalationPolicySteps(r.Context(), ep.ID, steps); err != nil {
		h.logger.Error("web: create escalation steps", "error", err)
	}

	h.setFlash(w, "Escalation policy created")
	h.redirect(w, r, "/escalation-policies")
}

func (h *Handler) EscalationPolicyUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/escalation-policies")
		return
	}

	ep, steps := h.parseEscalationPolicyForm(r)
	ep.ID = id

	if err := validate.ValidateEscalationPolicy(ep); err != nil {
		h.setFlash(w, err.Error())
		h.redirect(w, r, "/escalation-policies")
		return
	}

	if err := h.store.UpdateEscalationPolicy(r.Context(), ep); err != nil {
		h.logger.Error("web: update escalation policy", "error", err)
		h.setFlash(w, "Failed to update escalation policy")
		h.redirect(w, r, "/escalation-policies")
		return
	}

	if err := h.store.ReplaceEscalationPolicySteps(r.Context(), ep.ID, steps); err != nil {
		h.logger.Error("web: replace escalation steps", "error", err)
	}

	h.setFlash(w, "Escalation policy updated")
	h.redirect(w, r, "/escalation-policies")
}

func (h *Handler) EscalationPolicyDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/escalation-policies")
		return
	}
	if err := h.store.DeleteEscalationPolicy(r.Context(), id); err != nil {
		h.logger.Error("web: delete escalation policy", "error", err)
	}
	h.setFlash(w, "Escalation policy deleted")
	h.redirect(w, r, "/escalation-policies")
}

func (h *Handler) parseEscalationPolicyForm(r *http.Request) (*storage.EscalationPolicy, []*storage.EscalationPolicyStep) {
	r.ParseForm()

	ep := &storage.EscalationPolicy{
		Name:        strings.TrimSpace(r.FormValue("name")),
		Description: strings.TrimSpace(r.FormValue("description")),
		Enabled:     r.FormValue("enabled") == "on",
		Repeat:      r.FormValue("repeat") == "on",
	}

	var steps []*storage.EscalationPolicyStep
	delays := r.Form["step_delay_minutes[]"]
	channelsJSON := r.Form["step_channels[]"]

	for i := range delays {
		delay, _ := strconv.Atoi(delays[i])
		var channelIDs []int64
		if i < len(channelsJSON) {
			json.Unmarshal([]byte(channelsJSON[i]), &channelIDs)
		}
		if len(channelIDs) == 0 {
			continue
		}
		steps = append(steps, &storage.EscalationPolicyStep{
			StepOrder:              len(steps),
			DelayMinutes:           delay,
			NotificationChannelIDs: channelIDs,
		})
	}

	ep.Steps = steps
	return ep, steps
}

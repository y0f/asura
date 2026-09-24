package web

import (
	"net/http"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/web/views"
)

func (h *Handler) Agents(w http.ResponseWriter, r *http.Request) {
	h.renderAgents(w, r, nil)
}

// renderAgents renders the agent list. created, when set, is an agent that was
// just created: its token is shown once in a panel that stays until the user
// leaves the page, instead of in a self-dismissing toast.
func (h *Handler) renderAgents(w http.ResponseWriter, r *http.Request, created *storage.Agent) {
	agents, err := h.store.ListAgents(r.Context())
	if err != nil {
		h.logger.Error("web: list agents", "error", err)
	}
	lp := h.newLayoutParams(r, "Agents", "agents")
	params := views.AgentListParams{
		LayoutParams: lp,
		Agents:       agents,
		ServerURL:    h.cfg.ResolvedExternalURL(),
	}
	if created != nil {
		params.NewAgentName = created.Name
		params.NewAgentToken = created.Token
		w.Header().Set("Cache-Control", "no-store")
	}
	h.renderComponent(w, r, views.AgentListPage(params))
}

func (h *Handler) AgentCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	location := r.FormValue("location")
	if name == "" {
		h.setError(w, "Name is required")
		h.redirect(w, r, "/agents")
		return
	}
	a := &storage.Agent{Name: name, Location: location, Enabled: true}
	if err := h.store.CreateAgent(r.Context(), a); err != nil {
		h.logger.Error("web: create agent", "error", err)
		h.setError(w, "Failed to create agent")
		h.redirect(w, r, "/agents")
		return
	}
	h.audit(r, "create", "agent", a.ID, a.Name)
	h.renderAgents(w, r, a)
}

func (h *Handler) AgentDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/agents")
		return
	}
	if err := h.store.DeleteAgent(r.Context(), id); err != nil {
		h.logger.Error("web: delete agent", "error", err)
	}
	h.setFlash(w, "Agent deleted")
	h.redirect(w, r, "/agents")
}

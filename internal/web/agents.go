package web

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/web/views"
)

// agentReveal holds a newly created agent's token between the create POST and
// the redirected GET, so the token is shown exactly once and refreshing the
// page does not resubmit the form and create a second agent.
type agentReveal struct {
	name      string
	token     string
	owner     string
	createdAt time.Time
}

const agentRevealTTL = 10 * time.Minute

func (h *Handler) storeAgentReveal(r *http.Request, a *storage.Agent) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	id := hex.EncodeToString(b)
	h.revealMu.Lock()
	defer h.revealMu.Unlock()
	if h.agentReveals == nil {
		h.agentReveals = make(map[string]agentReveal)
	}
	for k, v := range h.agentReveals {
		if time.Since(v.createdAt) > agentRevealTTL {
			delete(h.agentReveals, k)
		}
	}
	h.agentReveals[id] = agentReveal{name: a.Name, token: a.Token, owner: httputil.GetAPIKeyName(r.Context()), createdAt: time.Now()}
	return id
}

// takeAgentReveal returns and forgets a pending reveal. It only matches the
// API key that created the agent, and only within agentRevealTTL.
func (h *Handler) takeAgentReveal(r *http.Request, id string) *storage.Agent {
	if id == "" {
		return nil
	}
	h.revealMu.Lock()
	defer h.revealMu.Unlock()
	v, ok := h.agentReveals[id]
	if !ok {
		return nil
	}
	delete(h.agentReveals, id)
	if time.Since(v.createdAt) > agentRevealTTL || v.owner != httputil.GetAPIKeyName(r.Context()) {
		return nil
	}
	return &storage.Agent{Name: v.name, Token: v.token}
}

func (h *Handler) Agents(w http.ResponseWriter, r *http.Request) {
	h.renderAgents(w, r, h.takeAgentReveal(r, r.URL.Query().Get("created")))
}

// renderAgents renders the agent list. created, when set, is an agent that was
// just created: its token is shown once in a panel instead of a toast.
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
	if id := h.storeAgentReveal(r, a); id != "" {
		h.redirect(w, r, "/agents?created="+id)
		return
	}
	h.setError(w, "Agent created, but its token could not be shown. Delete it and create it again.")
	h.redirect(w, r, "/agents")
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

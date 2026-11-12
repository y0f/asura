package web

import (
	"context"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/storage"
)

type unreadableAgentListStore struct{ storage.Store }

func (unreadableAgentListStore) ListAgents(context.Context) ([]*storage.Agent, error) {
	panic("agent creation must not query unrelated enrollments")
}

func TestAgentWriteOnlyCreationRevealsCommittedTokenWithoutListRead(t *testing.T) {
	h, store := storeHandler(t)
	h.cfg.Server.BasePath = "/console"
	hidden := &storage.Agent{Name: "hidden-existing-agent", Location: "private-location", Enabled: true}
	if err := store.CreateAgent(context.Background(), hidden); err != nil {
		t.Fatal(err)
	}
	h.store = unreadableAgentListStore{store}
	key := &config.APIKeyConfig{Name: "creator", Permissions: []string{"agents.create", "notification_history.read"}}
	r := adminRequest("POST", "/console/agents", url.Values{"name": {"new-agent"}, "location": {"test"}})
	r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, key))
	w := httptest.NewRecorder()
	h.AgentCreate(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), hidden.Name) || strings.Contains(w.Body.String(), hidden.Location) {
		t.Fatalf("write-only enrollment response status%d headers%v", w.Code, w.Header())
	}
	match := regexp.MustCompile(`--token ([a-f0-9]+)`).FindStringSubmatch(w.Body.String())
	if len(match) != 2 {
		t.Fatal("one-time enrollment token missing")
	}
	saved, err := store.GetAgentByToken(context.Background(), match[1])
	if err != nil || saved.Name != "new-agent" || strings.Contains(w.Body.String(), saved.Token) {
		t.Fatalf("revealed token is not bound to committed agent: %v", err)
	}
	if strings.Contains(w.Body.String(), `href="/console/agents"`) {
		t.Fatal("write-only creation gained agent list navigation")
	}
	if strings.Contains(w.Body.String(), "No agents yet") || !strings.Contains(w.Body.String(), "navigator.clipboard") || !strings.Contains(w.Body.String(), ".catch(") || !strings.Contains(w.Body.String(), `id="agent-command"`) {
		t.Fatal("one-time page lacks hidden-list boundary or clipboard fallback")
	}
	if len(h.agentReveals) != 0 {
		t.Fatal("direct one-time response also retained a replayable reveal")
	}
}

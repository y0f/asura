package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/v2/internal/api"
	"github.com/y0f/asura/v2/internal/checker"
	"github.com/y0f/asura/v2/internal/config"
	"github.com/y0f/asura/v2/internal/incident"
	"github.com/y0f/asura/v2/internal/monitor"
	"github.com/y0f/asura/v2/internal/storage"
)

func TestAgentResultsRequireCurrentJobRevision(t *testing.T) {
	for _, pipeline := range []bool{false, true} {
		t.Run(fmt.Sprintf("pipeline=%v", pipeline), func(t *testing.T) {
			env := newTestEnv(t, nil, pipeline, nil)
			a, m := agentMonitor(t, env)
			headers := map[string]string{"X-Agent-Token": a.Token}
			w := env.do("GET", "/api/v1/agent/jobs", "", headers)
			var response struct {
				Jobs []*storage.AgentJob `json:"jobs"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusOK || len(response.Jobs) != 1 {
				t.Fatalf("jobs: %d %s, %v", w.Code, w.Body.String(), err)
			}
			revision := response.Jobs[0].ConfigRevision
			if revision <= 0 {
				t.Fatal("job omitted its configuration revision")
			}
			for _, extra := range []string{"", `,"config_revision":0`, `,"config_revision":-1`, fmt.Sprintf(`,"config_revision":%d`, revision+1)} {
				w = env.do("POST", "/api/v1/agent/results", fmt.Sprintf(`[{"monitor_id":%d,"status":"down"%s}]`, m.ID, extra), headers)
				if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"accepted":0`) || !strings.Contains(w.Body.String(), `"rejected":1`) {
					t.Fatalf("invalid revision accepted: %d %s", w.Code, w.Body.String())
				}
			}
			m.Target = "https://changed.example.test"
			if err := env.store.UpdateMonitor(context.Background(), m); err != nil {
				t.Fatal(err)
			}
			w = env.do("POST", "/api/v1/agent/results", fmt.Sprintf(`[{"monitor_id":%d,"config_revision":%d,"status":"down"}]`, m.ID, revision), headers)
			if !strings.Contains(w.Body.String(), `"accepted":0`) || !strings.Contains(w.Body.String(), `"stale":1`) {
				t.Fatalf("obsolete job accepted: %s", w.Body.String())
			}
			assertNoAgentObservation(t, env.store, m.ID)
			current, err := env.store.GetMonitor(context.Background(), m.ID)
			if err != nil {
				t.Fatal(err)
			}
			w = env.do("POST", "/api/v1/agent/results", fmt.Sprintf(`[{"monitor_id":%d,"config_revision":%d,"status":"up"}]`, m.ID, current.ConfigRevision), headers)
			if !strings.Contains(w.Body.String(), `"accepted":1`) || !strings.Contains(w.Body.String(), `"rejected":0`) {
				t.Fatalf("current job rejected: %s", w.Body.String())
			}
		})
	}
}

func assertNoAgentObservation(t *testing.T, store storage.Store, id int64) {
	t.Helper()
	ctx := context.Background()
	checks, err := store.ListCheckResults(ctx, id, storage.Pagination{Page: 1, PerPage: 10})
	if err != nil || checks.Total != 0 {
		t.Fatalf("rejected job wrote history: %+v, %v", checks, err)
	}
	status, err := store.GetMonitorStatus(ctx, id)
	if err != nil || status.Status != "pending" || status.LastCheckAt != nil || status.ConsecFails != 0 || status.ConsecSuccesses != 0 || status.ConfigRevision != 0 {
		t.Fatalf("rejected job changed initial pending status: %+v, %v", status, err)
	}
	if _, err := store.GetOpenIncident(ctx, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rejected job opened incident: %v", err)
	}
}

func TestAgentResultCommitRechecksDelegationAndRevision(t *testing.T) {
	for _, pipeline := range []bool{false, true} {
		for _, change := range []string{"target", "assignment", "typed secret"} {
			t.Run(fmt.Sprintf("pipeline=%v/%s", pipeline, change), func(t *testing.T) {
				env := newTestEnv(t, nil, false, nil)
				a, m := agentMonitor(t, env)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				current, err := env.store.GetMonitor(ctx, m.ID)
				if err != nil {
					t.Fatal(err)
				}
				held := heldMonitorReadStore{Store: env.store, read: make(chan struct{}), resume: make(chan struct{})}
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				var p *monitor.Pipeline
				if pipeline {
					p = monitor.NewPipeline(held, checker.DefaultRegistry(nil, false), incident.NewManager(held, logger), 1, false, logger)
				}
				h := api.New(config.Defaults(), held, p, nil, logger, nil)
				body := fmt.Sprintf(`[{"monitor_id":%d,"config_revision":%d,"status":"down"}]`, m.ID, current.ConfigRevision)
				r := httptest.NewRequest("POST", "/api/v1/agent/results", strings.NewReader(body)).WithContext(ctx)
				r.Header.Set("X-Agent-Token", a.Token)
				w := httptest.NewRecorder()
				done := make(chan struct{})
				go func() { defer close(done); h.AgentAuth(http.HandlerFunc(h.AgentPostResults)).ServeHTTP(w, r) }()
				select {
				case <-held.read:
				case <-ctx.Done():
					t.Fatal("request never loaded job monitor")
				}
				switch change {
				case "target":
					m.Target = "https://changed.example.test"
					err = env.store.UpdateMonitor(ctx, m)
				case "assignment":
					err = env.store.SetMonitorAgentIDs(ctx, m.ID, nil)
				case "typed secret":
					m.Settings = json.RawMessage(`{"auth_method":"bearer","bearer_token":"private"}`)
					err = env.store.UpdateMonitor(ctx, m)
				}
				close(held.resume)
				select {
				case <-done:
				case <-ctx.Done():
					t.Fatal("request failed to finish")
				}
				if err != nil {
					t.Fatal(err)
				}
				if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"accepted":0`) || !strings.Contains(w.Body.String(), `"stale":1`) {
					t.Fatalf("stale job escaped transaction guard: %d %s", w.Code, w.Body.String())
				}
				assertNoAgentObservation(t, env.store, m.ID)
			})
		}
	}
}

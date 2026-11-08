package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/internal/api"
	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
)

func TestMonitorUpdatePreservesHiddenRelationsAndPauseStateAPI(t *testing.T) {
	env := newTestEnv(t, func(cfg *config.Config) {
		cfg.Auth.APIKeys = append(cfg.Auth.APIKeys, config.APIKeyConfig{Name: "editor", Hash: config.HashAPIKey("editor"), Permissions: []string{"monitors.read", "monitors.update"}})
	}, false, nil)
	ctx := context.Background()
	group := &storage.MonitorGroup{Name: "private group"}
	channel := &storage.NotificationChannel{Name: "private channel", Type: "webhook", Settings: []byte(`{"url":"https://example.test"}`)}
	agent := &storage.Agent{Name: "private agent", Enabled: true}
	for _, err := range []error{env.store.CreateMonitorGroup(ctx, group), env.store.CreateNotificationChannel(ctx, channel), env.store.CreateAgent(ctx, agent)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	m := &storage.Monitor{Name: "original", Type: "http", Target: "https://example.test", Interval: 60, Timeout: 10, FailureThreshold: 1, SuccessThreshold: 1,
		GroupID: &group.ID, AgentEnabled: true, AgentIDs: []int64{agent.ID}, NotificationChannelIDs: []int64{channel.ID}, Settings: []byte(`{"headers":{"Authorization":"private-token"}}`)}
	if err := env.store.CreateMonitorWithRelations(ctx, m, storage.MonitorCreateOptions{}); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/monitors/%d", m.ID)
	request := map[string]any{"name": "renamed", "type": "http", "target": m.Target, "interval": 60, "timeout": 10, "failure_threshold": 1, "success_threshold": 1, "settings": map[string]any{"headers": map[string]string{"Authorization": ""}}}
	body, _ := json.Marshal(request)
	w := env.do("PUT", path, string(body), map[string]string{"X-API-Key": "editor"})
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "private-token") {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	stored, err := env.store.GetMonitor(ctx, m.ID)
	ids, idsErr := env.store.GetMonitorNotificationChannelIDs(ctx, m.ID)
	agents, agentsErr := env.store.GetMonitorAgentIDs(ctx, m.ID)
	if err != nil || idsErr != nil || agentsErr != nil || stored.Enabled || stored.GroupID == nil || *stored.GroupID != group.ID || !stored.AgentEnabled || !reflect.DeepEqual(ids, []int64{channel.ID}) || !reflect.DeepEqual(agents, []int64{agent.ID}) || !strings.Contains(string(stored.Settings), "private-token") {
		t.Fatalf("omitted fields changed hidden state: %+v, channels=%v agents=%v errors=%v/%v/%v", stored, ids, agents, err, idsErr, agentsErr)
	}
	for _, field := range []string{"notification_channel_ids", "agent_ids", "group_id", "enabled"} {
		copy := make(map[string]any, len(request)+1)
		for key, value := range request {
			copy[key] = value
		}
		if field == "enabled" {
			copy[field] = true
		} else {
			copy[field] = nil
		}
		body, _ := json.Marshal(copy)
		w := env.do("PUT", path, string(body), map[string]string{"X-API-Key": "editor"})
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s mutation bypassed separate authority: %d %s", field, w.Code, w.Body.String())
		}
	}
}

func TestHeartbeatCreateCloneAndTypeBoundaryAPI(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	body := `{"name":"heartbeat","type":"heartbeat","interval":60,"timeout":10,"settings":{"grace":5}}`
	w := env.do("POST", "/api/v1/monitors", body, admin)
	if w.Code != http.StatusCreated || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Monitor   storage.Monitor   `json:"monitor"`
		Heartbeat storage.Heartbeat `json:"heartbeat"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Heartbeat.Token == "" || created.Heartbeat.MonitorID != created.Monitor.ID {
		t.Fatalf("missing one-time credential: %+v", created)
	}
	path := fmt.Sprintf("/api/v1/monitors/%d", created.Monitor.ID)
	w = env.do("POST", path+"/clone", "", admin)
	if w.Code != http.StatusCreated {
		t.Fatalf("clone: %d %s", w.Code, w.Body.String())
	}
	var cloned struct {
		Monitor   storage.Monitor   `json:"monitor"`
		Heartbeat storage.Heartbeat `json:"heartbeat"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cloned); err != nil {
		t.Fatal(err)
	}
	if cloned.Heartbeat.Token == "" || cloned.Heartbeat.Token == created.Heartbeat.Token || cloned.Heartbeat.MonitorID != cloned.Monitor.ID || cloned.Monitor.Enabled {
		t.Fatalf("clone reused credentials or enabled itself: %+v", cloned)
	}
	created.Monitor.Type, created.Monitor.Target = "http", "https://example.test"
	mutation, _ := json.Marshal(created.Monitor)
	w = env.do("PUT", path, string(mutation), admin)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "heartbeat monitor type cannot be changed") {
		t.Fatalf("unsafe type transition: %d %s", w.Code, w.Body.String())
	}
	stored, err := env.store.GetMonitor(context.Background(), created.Monitor.ID)
	if err != nil || stored.Type != "heartbeat" {
		t.Fatalf("rejected transition changed type: %+v, %v", stored, err)
	}
	for _, endpoint := range []string{"/api/v1/monitors", path} {
		w := env.do("GET", endpoint, "", admin)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), created.Heartbeat.Token) || strings.Contains(w.Body.String(), cloned.Heartbeat.Token) {
			t.Fatalf("ordinary read disclosed bearer credential: %d %s", w.Code, w.Body.String())
		}
	}
}

// Request handlers enforce object scopes even before route capability admission
// is enabled. Full middleware/session coverage belongs to the route matrix.
func TestMonitorHandlersEnforceActionScopes(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	ctx := context.Background()
	groups := []*storage.MonitorGroup{{Name: "allowed"}, {Name: "other"}}
	monitors := make([]*storage.Monitor, 2)
	for i, group := range groups {
		if err := env.store.CreateMonitorGroup(ctx, group); err != nil {
			t.Fatal(err)
		}
		monitors[i] = &storage.Monitor{Name: group.Name, Type: "http", Target: "https://example.test", Interval: 60, Timeout: 10, FailureThreshold: 1, SuccessThreshold: 1, Enabled: true, GroupID: &group.ID}
		if err := env.store.CreateMonitor(ctx, monitors[i]); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Defaults()
	cfg.Auth.Roles = []config.RoleConfig{{Name: "monitor-editor", Permissions: []string{"monitors.read", "monitors.write"}}}
	cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "scoped", Hash: config.HashAPIKey("scoped"), Permissions: []string{"groups.read"}, RoleBindings: []config.RoleBindingConfig{{Role: "monitor-editor", Scope: config.MonitorAccess{GroupIDs: []int64{groups[0].ID}}}}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	key := &cfg.Auth.APIKeys[0]
	h := api.New(cfg, env.store, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	do := func(handler http.HandlerFunc, method string, id int64, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1/monitors/"+strconv.FormatInt(id, 10), strings.NewReader(body))
		r.SetPathValue("id", strconv.FormatInt(id, 10))
		r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, key))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	body := `{"name":"changed","type":"http","target":"https://example.test","interval":60,"timeout":10,"failure_threshold":1,"success_threshold":1}`
	for _, operation := range []struct {
		name, method string
		handler      http.HandlerFunc
		body         string
	}{
		{"read", "GET", h.GetMonitor, ""}, {"checks", "GET", h.ListChecks, ""}, {"changes", "GET", h.ListChanges, ""},
		{"update", "PUT", h.UpdateMonitor, body}, {"pause", "POST", h.PauseMonitor, ""}, {"resume", "POST", h.ResumeMonitor, ""},
		{"delete", "DELETE", h.DeleteMonitor, ""}, {"clone", "POST", h.CloneMonitor, ""},
	} {
		t.Run(operation.name, func(t *testing.T) {
			w := do(operation.handler, operation.method, monitors[1].ID, operation.body)
			if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), monitors[1].Name) {
				t.Fatalf("outside scope: %d %s", w.Code, w.Body.String())
			}
		})
	}
	w := do(h.ListMonitors, "GET", 0, "")
	var list struct {
		Total int64              `json:"total"`
		Data  []*storage.Monitor `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != http.StatusOK || list.Total != 1 || len(list.Data) != 1 || list.Data[0].ID != monitors[0].ID {
		t.Fatalf("scoped list/count: %d %s (%v)", w.Code, w.Body.String(), err)
	}
	w = do(h.BulkMonitors, "POST", 0, fmt.Sprintf(`{"action":"pause","ids":[%d,%d]}`, monitors[0].ID, monitors[1].ID))
	if w.Code != http.StatusNotFound {
		t.Fatalf("mixed-scope bulk: %d %s", w.Code, w.Body.String())
	}
	for _, m := range monitors {
		stored, err := env.store.GetMonitor(ctx, m.ID)
		if err != nil || !stored.Enabled || stored.Name != m.Name {
			t.Fatalf("denied operation changed monitor: %+v %v", stored, err)
		}
	}
	moving := strings.TrimSuffix(body, "}") + fmt.Sprintf(`,"group_id":%d}`, groups[1].ID)
	w = do(h.UpdateMonitor, "PUT", monitors[0].ID, moving)
	if w.Code != http.StatusForbidden {
		t.Fatalf("move outside destination scope: %d %s", w.Code, w.Body.String())
	}
	for _, group := range []*int64{nil, &groups[1].ID, &groups[0].ID} {
		request := map[string]any{"name": "created", "type": "http", "target": "https://example.test", "group_id": group}
		payload, _ := json.Marshal(request)
		w = do(h.CreateMonitor, "POST", 0, string(payload))
		want := http.StatusNotFound
		if group != nil && *group == groups[0].ID {
			want = http.StatusCreated
		}
		if w.Code != want {
			t.Fatalf("create destination %v: %d %s; want %d", group, w.Code, w.Body.String(), want)
		}
	}
	// Current membership, rather than the membership at session creation,
	// controls subsequent access.
	monitors[0].GroupID = &groups[1].ID
	if err := env.store.UpdateMonitor(ctx, monitors[0]); err != nil {
		t.Fatal(err)
	}
	if w = do(h.GetMonitor, "GET", monitors[0].ID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("moved object retained old group access: %d %s", w.Code, w.Body.String())
	}
}

type heldMonitorReadStore struct {
	storage.Store
	read   chan struct{}
	resume chan struct{}
}

func (s heldMonitorReadStore) GetMonitor(ctx context.Context, id int64, access ...storage.MonitorAccess) (*storage.Monitor, error) {
	monitor, err := s.Store.GetMonitor(ctx, id, access...)
	close(s.read)
	select {
	case <-s.resume:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return monitor, err
}

func TestMonitorNestedReadsRejectGroupRevocationAfterLookup(t *testing.T) {
	for _, endpoint := range []string{"monitor", "checks", "changes", "metrics", "chart"} {
		t.Run(endpoint, func(t *testing.T) {
			env := newTestEnv(t, nil, false, nil)
			ctx := context.Background()
			group := &storage.MonitorGroup{Name: "allowed"}
			if err := env.store.CreateMonitorGroup(ctx, group); err != nil {
				t.Fatal(err)
			}
			monitor := &storage.Monitor{Name: "private-monitor-name", Type: "http", Target: "https://example.test", GroupID: &group.ID, Enabled: true, Interval: 60, Timeout: 10}
			if err := env.store.CreateMonitor(ctx, monitor); err != nil {
				t.Fatal(err)
			}
			if err := env.store.InsertCheckResult(ctx, &storage.CheckResult{MonitorID: monitor.ID, Status: "up", Message: "private-check", ResponseTime: 123}); err != nil {
				t.Fatal(err)
			}
			if err := env.store.InsertContentChange(ctx, &storage.ContentChange{MonitorID: monitor.ID, OldHash: "old", NewHash: "new", Diff: "private-change"}); err != nil {
				t.Fatal(err)
			}
			cfg := config.Defaults()
			cfg.Auth.Roles = []config.RoleConfig{{Name: "reader", Permissions: []string{"monitors.read"}}}
			cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "reader", Hash: config.HashAPIKey("reader"), RoleBindings: []config.RoleBindingConfig{{Role: "reader", Scope: config.MonitorAccess{GroupIDs: []int64{group.ID}}}}}}
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			held := heldMonitorReadStore{Store: env.store, read: make(chan struct{}), resume: make(chan struct{})}
			h := api.New(cfg, held, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
			handler := map[string]http.HandlerFunc{"monitor": h.GetMonitor, "checks": h.ListChecks, "changes": h.ListChanges, "metrics": h.MonitorMetrics, "chart": h.MonitorChart}[endpoint]
			requestCtx, cancel := context.WithTimeout(context.WithValue(ctx, httputil.CtxKeyAPIKey, &cfg.Auth.APIKeys[0]), 5*time.Second)
			defer cancel()
			r := httptest.NewRequest("GET", "/api/v1/monitors/"+strconv.FormatInt(monitor.ID, 10), nil).WithContext(requestCtx)
			r.SetPathValue("id", strconv.FormatInt(monitor.ID, 10))
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); handler(w, r) }()
			select {
			case <-held.read:
			case <-requestCtx.Done():
				t.Fatal("handler never reached authorized lookup")
			}
			monitor.GroupID = nil
			if err := env.store.UpdateMonitor(ctx, monitor); err != nil {
				close(held.resume)
				<-done
				t.Fatal(err)
			}
			close(held.resume)
			select {
			case <-done:
			case <-requestCtx.Done():
				t.Fatal("handler did not finish")
			}
			if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "private-") {
				t.Fatalf("nested read used revoked group scope: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

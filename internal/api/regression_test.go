package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/y0f/asura/internal/checker"
	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/incident"
	"github.com/y0f/asura/internal/monitor"
	"github.com/y0f/asura/internal/notifier"
	"github.com/y0f/asura/internal/server"
	"github.com/y0f/asura/internal/storage"
)

const testAdminKey = "regression-admin-key"

var admin = map[string]string{"X-API-Key": testAdminKey}

type testEnv struct {
	srv    *server.Server
	store  storage.Store
	dbPath string
}

func newTestEnv(t *testing.T, mutate func(*config.Config), withPipeline bool, subNotifier func(storage.Store, *config.Config) *notifier.SubscriberNotifier) *testEnv {
	t.Helper()
	f, err := os.CreateTemp("", "asura-api-regression-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })
	store, err := storage.NewSQLiteStore(f.Name(), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	cfg := config.Defaults()
	cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "admin", Hash: config.HashAPIKey(testAdminKey), SuperAdmin: true}}
	if mutate != nil {
		mutate(cfg)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var p *monitor.Pipeline
	if withPipeline {
		p = monitor.NewPipeline(store, checker.DefaultRegistry(nil, false), incident.NewManager(store, logger), 1, false, logger)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-p.NotifyChan():
				}
			}
		}()
	}
	var sn *notifier.SubscriberNotifier
	if subNotifier != nil {
		sn = subNotifier(store, cfg)
	}
	dispatcher := notifier.NewDispatcher(store, logger, true)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		dispatcher.Shutdown(ctx)
		if sn != nil {
			sn.Shutdown(ctx)
		}
	})
	srv := server.NewServer(cfg, store, p, dispatcher, sn, logger, "test")
	t.Cleanup(srv.Close)
	return &testEnv{srv: srv, store: store, dbPath: f.Name()}
}

func headless(c *config.Config) {
	f := false
	c.Server.WebUIEnabled = &f
}

func (e *testEnv) do(method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.srv.ServeHTTP(w, req)
	return w
}

// createStatusPage creates a page through the API so the server's slug routes
// are refreshed.
func (e *testEnv) createStatusPage(t *testing.T, body string) *storage.StatusPage {
	t.Helper()
	w := e.do("POST", "/api/v1/status-pages", body, admin)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status page: %d %s", w.Code, w.Body.String())
	}
	var sp storage.StatusPage
	if err := json.Unmarshal(w.Body.Bytes(), &sp); err != nil {
		t.Fatal(err)
	}
	return &sp
}

// API email subscriptions send a confirmation email, and headless the
// confirmation link works.
func TestAPIEmailSubscribeSendsConfirmation(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var smtpConns int64
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			atomic.AddInt64(&smtpConns, 1)
			c.Write([]byte("421 test server closing\r\n"))
			c.Close()
		}
	}()
	port, _ := strconv.Atoi(strings.Split(ln.Addr().String(), ":")[1])

	env := newTestEnv(t, func(c *config.Config) {
		headless(c)
		c.Subscriptions.Enabled = true
		c.Subscriptions.SMTP = config.SMTPConfig{Host: "127.0.0.1", Port: port, From: "asura@example.com", TLSMode: "none"}
	}, false, func(s storage.Store, c *config.Config) *notifier.SubscriberNotifier {
		return notifier.NewSubscriberNotifier(s, c.Subscriptions.SMTP, "http://localhost", slog.New(slog.NewTextHandler(io.Discard, nil)), true)
	})
	ctx := context.Background()
	sp := env.createStatusPage(t, `{"slug":"s1","title":"S1","enabled":true,"api_enabled":true}`)

	w := env.do("POST", "/api/v1/status-pages/"+strconv.FormatInt(sp.ID, 10)+"/subscribe", `{"type":"email","email":"a@example.com"}`, nil)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), "confirmation email queued") {
		t.Fatalf("subscribe: %d %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt64(&smtpConns) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if atomic.LoadInt64(&smtpConns) == 0 {
		t.Errorf("API answered %q but no SMTP connection was attempted", strings.TrimSpace(w.Body.String()))
	}

	db, err := sql.Open("sqlite", "file:"+env.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var token string
	if err := db.QueryRow(`SELECT token FROM status_page_subscribers WHERE email = ?`, "a@example.com").Scan(&token); err != nil {
		t.Fatal(err)
	}
	if w := env.do("GET", "/s1/confirm?token="+token, "", nil); w.Code != http.StatusOK {
		t.Fatalf("headless confirm link: %d %s", w.Code, w.Body.String())
	}
	subs, _ := env.store.ListConfirmedSubscribers(ctx, sp.ID)
	if len(subs) != 1 {
		t.Errorf("confirmed subscribers = %d, want 1", len(subs))
	}
}

// The public JSON is gated as in v1.3.0: it is served when the page is
// enabled or api_enabled is on, and only hidden when both are off.
func TestPublicStatusPageGating(t *testing.T) {
	env := newTestEnv(t, headless, false, nil)
	for i, tc := range []struct {
		enabled, apiEnabled bool
		want                int
	}{
		{true, false, http.StatusOK},
		{false, true, http.StatusOK},
		{true, true, http.StatusOK},
		{false, false, http.StatusNotFound},
	} {
		sp := env.createStatusPage(t, fmt.Sprintf(`{"slug":"p%d","title":"P","enabled":%v,"api_enabled":%v}`, i, tc.enabled, tc.apiEnabled))
		w := env.do("GET", "/api/v1/status-pages/"+strconv.FormatInt(sp.ID, 10)+"/public", "", nil)
		if w.Code != tc.want {
			t.Errorf("enabled=%v api_enabled=%v: /public returned %d %s, want %d", tc.enabled, tc.apiEnabled, w.Code, strings.TrimSpace(w.Body.String()), tc.want)
		}
	}
}

// The API can set, use and clear a status page password without the web UI.
func TestStatusPagePasswordViaAPI(t *testing.T) {
	env := newTestEnv(t, headless, false, nil)
	sp := env.createStatusPage(t, `{"slug":"locked","title":"Locked","enabled":true,"api_enabled":true,"password":"s3cret"}`)
	id := strconv.FormatInt(sp.ID, 10)
	if !sp.PasswordEnabled {
		t.Error("create with password: password_enabled=false")
	}

	if w := env.do("GET", "/api/v1/status-pages/"+id+"/public", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no password: got %d, want 401", w.Code)
	}
	if w := env.do("GET", "/api/v1/status-pages/"+id+"/public", "", map[string]string{"X-Status-Page-Password": "wrong"}); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: got %d, want 401", w.Code)
	}
	if w := env.do("GET", "/api/v1/status-pages/"+id+"/public", "", map[string]string{"X-Status-Page-Password": "s3cret"}); w.Code != http.StatusOK {
		t.Errorf("password header: got %d %s, want 200", w.Code, w.Body.String())
	}

	// An update without "password" keeps it.
	if w := env.do("PUT", "/api/v1/status-pages/"+id, `{"slug":"locked","title":"Locked 2","enabled":true,"api_enabled":true}`, admin); w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if w := env.do("GET", "/api/v1/status-pages/"+id+"/public", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("password dropped by an update without the field: got %d", w.Code)
	}

	// "password": "" clears it.
	if w := env.do("PUT", "/api/v1/status-pages/"+id, `{"slug":"locked","title":"Locked","enabled":true,"api_enabled":true,"password":""}`, admin); w.Code != http.StatusOK {
		t.Fatalf("clear password: %d %s", w.Code, w.Body.String())
	}
	if w := env.do("GET", "/api/v1/status-pages/"+id+"/public", "", nil); w.Code != http.StatusOK {
		t.Errorf("after clearing password: got %d, want 200", w.Code)
	}
}

// Enabled subscriptions require both page gates. Webhooks need no SMTP;
// email registration fails visibly when confirmation transport is unavailable.
func TestSubscribeAPIGating(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) {
		c.Subscriptions.Enabled = true
		c.Monitor.AllowPrivateTargets = true // no DNS lookup of the webhook host
	}, false, nil)
	ctx := context.Background()
	for i, tc := range []struct {
		enabled, apiEnabled bool
		want                int
	}{
		{true, true, http.StatusCreated},
		{true, false, http.StatusNotFound},
		{false, true, http.StatusNotFound},
	} {
		sp := env.createStatusPage(t, fmt.Sprintf(`{"slug":"s%d","title":"S","enabled":%v,"api_enabled":%v}`, i, tc.enabled, tc.apiEnabled))
		path := "/api/v1/status-pages/" + strconv.FormatInt(sp.ID, 10) + "/subscribe"
		for _, body := range []string{`{"type":"webhook","webhook_url":"https://example.com/hook"}`, `{"type":"email","email":"a@example.com"}`} {
			want := tc.want
			if want == http.StatusCreated && strings.Contains(body, `"type":"email"`) {
				want = http.StatusServiceUnavailable
			}
			if w := env.do("POST", path, body, nil); w.Code != want {
				t.Errorf("enabled=%v api_enabled=%v: subscribe %s returned %d %s, want %d", tc.enabled, tc.apiEnabled, body, w.Code, strings.TrimSpace(w.Body.String()), want)
			}
		}
		wantN := int64(0) // Only the webhook is stored; unavailable email can be retried.
		if tc.want == http.StatusCreated {
			wantN = 1
		}
		if n, _ := env.store.CountSubscribersByPage(ctx, sp.ID); n != wantN {
			t.Errorf("enabled=%v api_enabled=%v: %d subscriber(s) stored, want %d", tc.enabled, tc.apiEnabled, n, wantN)
		}
	}
}

func agentMonitor(t *testing.T, env *testEnv) (*storage.Agent, *storage.Monitor) {
	t.Helper()
	ctx := context.Background()
	a := &storage.Agent{Name: "probe", Enabled: true}
	if err := env.store.CreateAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	m := &storage.Monitor{
		Name: "prod-api", Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10,
		Enabled: true, AgentEnabled: true, Tags: []string{}, FailureThreshold: 1, SuccessThreshold: 1,
	}
	if err := env.store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := env.store.SetMonitorAgentIDs(ctx, m.ID, []int64{a.ID}); err != nil {
		t.Fatal(err)
	}
	return a, m
}

// postResults models a current v2 job for older behavioral fixtures. Protocol
// tests send raw requests so missing, invalid and stale revisions stay explicit.
func postResults(t *testing.T, env *testEnv, a *storage.Agent, body string) *httptest.ResponseRecorder {
	t.Helper()
	var results []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &results); err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if _, present := result["config_revision"]; present {
			continue
		}
		var id int64
		if err := json.Unmarshal(result["monitor_id"], &id); err != nil {
			t.Fatal(err)
		}
		monitor, err := env.store.GetMonitor(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		result["config_revision"] = json.RawMessage(strconv.FormatInt(monitor.ConfigRevision, 10))
	}
	encoded, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	body = string(encoded)
	return env.do("POST", "/api/v1/agent/results", body, map[string]string{"X-Agent-Token": a.Token})
}

// A monitor assigned to other agents accepts results only from those agents,
// so one agent's token cannot write results for monitors it does not run.
func TestAgentResultsRequireAssignment(t *testing.T) {
	env := newTestEnv(t, nil, true, nil)
	ctx := context.Background()
	a, m := agentMonitor(t, env)
	other := &storage.Agent{Name: "other-probe", Enabled: true}
	if err := env.store.CreateAgent(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := env.store.SetMonitorAgentIDs(ctx, m.ID, []int64{other.ID}); err != nil {
		t.Fatal(err)
	}

	w := postResults(t, env, a, `[{"monitor_id":`+strconv.FormatInt(m.ID, 10)+`,"status":"down","message":"forged"}]`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"accepted":0`) {
		t.Fatalf("unassigned agent post: %d %s, want accepted 0", w.Code, w.Body.String())
	}
	if inc, err := env.store.GetOpenIncident(ctx, m.ID); err == nil && inc != nil {
		t.Errorf("unassigned agent %q opened incident %d on monitor %d", a.Name, inc.ID, m.ID)
	}

	w = postResults(t, env, other, `[{"monitor_id":`+strconv.FormatInt(m.ID, 10)+`,"status":"up"}]`)
	if !strings.Contains(w.Body.String(), `"accepted":1`) {
		t.Fatalf("assigned agent post: %d %s, want accepted 1", w.Code, w.Body.String())
	}
}

// Jobs follow explicit assignments; new agents receive no jobs automatically.
func TestAgentJobsFollowAssignment(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	ctx := context.Background()
	a, open := agentMonitor(t, env)
	other := &storage.Agent{Name: "other-probe", Enabled: true}
	if err := env.store.CreateAgent(ctx, other); err != nil {
		t.Fatal(err)
	}
	pinned := &storage.Monitor{
		Name: "pinned", Type: "http", Target: "https://example.org", Interval: 60, Timeout: 10,
		Enabled: true, AgentEnabled: true, Tags: []string{}, FailureThreshold: 1, SuccessThreshold: 1,
	}
	if err := env.store.CreateMonitor(ctx, pinned); err != nil {
		t.Fatal(err)
	}
	if err := env.store.SetMonitorAgentIDs(ctx, pinned.ID, []int64{other.ID}); err != nil {
		t.Fatal(err)
	}
	ids := func(ag *storage.Agent) []int64 {
		jobs, err := env.store.ListAgentJobs(ctx, ag.ID)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, j := range jobs {
			out = append(out, j.ID)
		}
		return out
	}
	if got := ids(a); len(got) != 1 || got[0] != open.ID {
		t.Errorf("agent %q jobs = %v, want only assigned monitor %d", a.Name, got, open.ID)
	}
	if got := ids(other); len(got) != 1 || got[0] != pinned.ID {
		t.Errorf("agent %q jobs = %v, want only assigned monitor %d", other.Name, got, pinned.ID)
	}
	newAgent := &storage.Agent{Name: "new-probe", Enabled: true}
	if err := env.store.CreateAgent(ctx, newAgent); err != nil {
		t.Fatal(err)
	}
	if got := ids(newAgent); len(got) != 0 {
		t.Errorf("new agent received jobs without explicit assignment: %v", got)
	}
}

// agent_ids round-trips through the monitor API and unknown agents are rejected.
func TestMonitorAgentIDsAPI(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	ctx := context.Background()
	a := &storage.Agent{Name: "probe", Enabled: true}
	if err := env.store.CreateAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(a.ID, 10)
	body := `{"name":"api","type":"http","target":"https://example.com","interval":60,"timeout":10,"agent_ids":[` + id + `]}`
	w := env.do("POST", "/api/v1/monitors", body, admin)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created storage.Monitor
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	w = env.do("GET", "/api/v1/monitors/"+strconv.FormatInt(created.ID, 10), "", admin)
	if !strings.Contains(w.Body.String(), `"agent_ids":[`+id+`]`) {
		t.Fatalf("get: %s, want agent_ids [%s]", w.Body.String(), id)
	}
	w = env.do("POST", "/api/v1/monitors", `{"name":"bad","type":"http","target":"https://example.com","interval":60,"timeout":10,"agent_ids":[999999]}`, admin)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown agent: %d %s, want 400", w.Code, w.Body.String())
	}

	monID := strconv.FormatInt(created.ID, 10)
	put := func(agentIDs string) {
		t.Helper()
		body := `{"name":"api","type":"http","target":"https://example.com","interval":60,"timeout":10,"failure_threshold":1,"success_threshold":1,"agent_ids":` + agentIDs + `}`
		if w := env.do("PUT", "/api/v1/monitors/"+monID, body, admin); w.Code != http.StatusOK {
			t.Fatalf("update agent_ids=%s: %d %s", agentIDs, w.Code, w.Body.String())
		}
	}
	stored := func(monitorID int64) []int64 {
		t.Helper()
		ids, err := env.store.GetMonitorAgentIDs(ctx, monitorID)
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}

	// PUT replaces the assignment.
	b := &storage.Agent{Name: "probe-2", Enabled: true}
	if err := env.store.CreateAgent(ctx, b); err != nil {
		t.Fatal(err)
	}
	put(`[` + strconv.FormatInt(b.ID, 10) + `]`)
	if got := stored(created.ID); len(got) != 1 || got[0] != b.ID {
		t.Fatalf("PUT agent_ids [%d]: stored %v", b.ID, got)
	}

	// Clone copies it.
	w = env.do("POST", "/api/v1/monitors/"+monID+"/clone", "", admin)
	if w.Code != http.StatusCreated {
		t.Fatalf("clone: %d %s", w.Code, w.Body.String())
	}
	var clone storage.Monitor
	if err := json.Unmarshal(w.Body.Bytes(), &clone); err != nil {
		t.Fatal(err)
	}
	if got := stored(clone.ID); len(got) != 1 || got[0] != b.ID {
		t.Errorf("clone agent_ids = %v, want [%d]", got, b.ID)
	}

	// An unknown agent in a PUT is rejected and keeps the assignment.
	if w := env.do("PUT", "/api/v1/monitors/"+monID, `{"name":"api","type":"http","target":"https://example.com","interval":60,"timeout":10,"failure_threshold":1,"success_threshold":1,"agent_ids":[999999]}`, admin); w.Code != http.StatusBadRequest {
		t.Errorf("PUT unknown agent: %d %s, want 400", w.Code, w.Body.String())
	}
	if got := stored(created.ID); len(got) != 1 || got[0] != b.ID {
		t.Errorf("rejected PUT changed the assignment: %v", got)
	}

	// An empty list clears it, so GET omits agent_ids.
	put(`[]`)
	if got := stored(created.ID); len(got) != 0 {
		t.Fatalf("PUT agent_ids []: stored %v, want none", got)
	}
	w = env.do("GET", "/api/v1/monitors/"+monID, "", admin)
	if strings.Contains(w.Body.String(), `"agent_ids"`) {
		t.Errorf("cleared assignment still in GET: %s", w.Body.String())
	}
}

// Posting results counts as a heartbeat.
func TestAgentPostingResultsKeepsAgentOnline(t *testing.T) {
	env := newTestEnv(t, nil, true, nil)
	a, m := agentMonitor(t, env)

	db, err := sql.Open("sqlite", "file:"+env.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Last job fetch 3 minutes ago (one long check cycle).
	if _, err := db.Exec(`UPDATE agents SET last_heartbeat=? WHERE id=?`, time.Now().Add(-3*time.Minute).UTC().Format("2006-01-02T15:04:05Z"), a.ID); err != nil {
		t.Fatal(err)
	}
	w := postResults(t, env, a, `[{"monitor_id":`+strconv.FormatInt(m.ID, 10)+`,"status":"up"}]`)
	if !strings.Contains(w.Body.String(), `"accepted":1`) {
		t.Fatalf("post: %s", w.Body.String())
	}
	w = env.do("GET", "/api/v1/agents/status", "", admin)
	var resp struct {
		Agents []struct {
			ID       int64  `json:"id"`
			Online   bool   `json:"online"`
			LastSeen string `json:"last_seen"`
		} `json:"agents"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	found := false
	for _, s := range resp.Agents {
		if s.ID == a.ID {
			found = true
			if !s.Online {
				t.Errorf("agent just posted results but /agents/status reports online=false (last_seen=%s)", s.LastSeen)
			}
		}
	}
	if !found {
		t.Fatalf("agent missing from status: %s", w.Body.String())
	}
}

// An agent's result checked before its own newest applied result is stored
// but must not change status or reopen a resolved incident.
func TestStaleAgentResultDoesNotReopenIncident(t *testing.T) {
	env := newTestEnv(t, nil, true, nil)
	ctx := context.Background()
	a, m := agentMonitor(t, env)
	post := func(status string, checkedAt time.Time) {
		t.Helper()
		body, _ := json.Marshal([]map[string]any{{"monitor_id": m.ID, "status": status, "message": "timeout", "checked_at": checkedAt.UTC().Format(time.RFC3339Nano)}})
		if w := postResults(t, env, a, string(body)); !strings.Contains(w.Body.String(), `"accepted":1`) {
			t.Fatalf("post: %s", w.Body.String())
		}
	}

	// The agent saw the target down, then up again (resolving the incident).
	post("down", time.Now().Add(-3*time.Minute))
	inc, err := env.store.GetOpenIncident(ctx, m.ID)
	if err != nil || inc == nil {
		t.Fatalf("setup: no incident after agent 'down': %v", err)
	}
	post("up", time.Now().Add(-time.Minute))
	if open, err := env.store.GetOpenIncident(ctx, m.ID); err == nil && open != nil {
		t.Fatalf("setup: incident %d not resolved", open.ID)
	}

	// A delayed "down" the agent observed between the two arrives last.
	post("down", time.Now().Add(-2*time.Minute))
	if reopened, err := env.store.GetOpenIncident(ctx, m.ID); err == nil && reopened != nil {
		t.Errorf("agent 'down' checked before its own newer 'up' opened incident %d (resolved: %d)", reopened.ID, inc.ID)
	}
	if st, _ := env.store.GetMonitorStatus(ctx, m.ID); st == nil || st.Status != "up" {
		t.Errorf("stale agent result changed monitor status: %+v", st)
	}

	// A newer result is still applied.
	post("down", time.Now())
	if st, _ := env.store.GetMonitorStatus(ctx, m.ID); st == nil || st.Status != "down" {
		t.Errorf("fresh agent result not applied: %+v", st)
	}
}

func lastCheckResult(t *testing.T, env *testEnv, monitorID int64) *storage.CheckResult {
	t.Helper()
	cr, err := env.store.GetLatestCheckResult(context.Background(), monitorID)
	if err != nil {
		t.Fatal(err)
	}
	return cr
}

// An agent result is stored with the evaluated status (upside_down,
// assertions), so history and uptime agree with the monitor status.
func TestAgentResultStoredAfterEvaluation(t *testing.T) {
	env := newTestEnv(t, nil, true, nil)
	ctx := context.Background()
	a, m := agentMonitor(t, env)
	m.UpsideDown = true
	if err := env.store.UpdateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	w := postResults(t, env, a, `[{"monitor_id":`+strconv.FormatInt(m.ID, 10)+`,"status":"down","message":"refused"}]`)
	if !strings.Contains(w.Body.String(), `"accepted":1`) {
		t.Fatalf("post: %s", w.Body.String())
	}
	st, _ := env.store.GetMonitorStatus(ctx, m.ID)
	if cr := lastCheckResult(t, env, m.ID); cr.Status != "up" || st == nil || st.Status != "up" {
		t.Errorf("upside-down monitor: stored check status %q, monitor status %+v; want both up", cr.Status, st)
	}
}

// Staleness is judged per location: a result is not dropped because another
// location (here the local worker) reported after the agent ran its check,
// but an agent's older result does not override its own newer one.
func TestAgentResultStalenessIsPerLocation(t *testing.T) {
	env := newTestEnv(t, nil, true, nil)
	ctx := context.Background()
	a, m := agentMonitor(t, env)
	post := func(status string, checkedAt time.Time) {
		t.Helper()
		body, _ := json.Marshal([]map[string]any{{"monitor_id": m.ID, "status": status, "checked_at": checkedAt.UTC().Format(time.RFC3339Nano)}})
		if w := postResults(t, env, a, string(body)); !strings.Contains(w.Body.String(), `"accepted":1`) {
			t.Fatalf("post: %s", w.Body.String())
		}
	}
	status := func() string {
		st, _ := env.store.GetMonitorStatus(ctx, m.ID)
		if st == nil {
			return ""
		}
		return st.Status
	}

	// The local worker processed a check 10s ago; the agent checked 30s ago.
	localCheck := time.Now().Add(-10 * time.Second)
	env.store.UpsertMonitorStatus(ctx, &storage.MonitorStatus{MonitorID: m.ID, Status: "up", ConsecSuccesses: 1, LastCheckAt: &localCheck})
	post("down", time.Now().Add(-30*time.Second))
	if got := status(); got != "down" {
		t.Fatalf("agent result checked before another location's check was dropped: status %q, want down", got)
	}

	// The same agent's result from before its last one is stale.
	post("up", time.Now().Add(-60*time.Second))
	if got := status(); got != "down" {
		t.Errorf("older result from the same agent overrode its newer one: status %q, want down", got)
	}
	if res, err := env.store.ListCheckResults(ctx, m.ID, storage.Pagination{Page: 1, PerPage: 10}); err != nil || res.Total != 2 {
		t.Errorf("stale result not kept in history: %+v %v", res, err)
	}
}

// checked_at has no age limit: an agent with a long cycle posts results
// checked minutes ago, and with nothing newer from that agent they drive the
// status. A future checked_at counts as the receive time, so it cannot make
// the agent's later results look stale.
func TestAgentCheckedAtLongCycleAndFuture(t *testing.T) {
	env := newTestEnv(t, nil, true, nil)
	ctx := context.Background()
	a, m := agentMonitor(t, env)
	post := func(status string, checkedAt *time.Time) {
		t.Helper()
		res := map[string]any{"monitor_id": m.ID, "status": status}
		if checkedAt != nil {
			res["checked_at"] = checkedAt.UTC().Format(time.RFC3339)
		}
		body, _ := json.Marshal([]map[string]any{res})
		if w := postResults(t, env, a, string(body)); !strings.Contains(w.Body.String(), `"accepted":1`) {
			t.Fatalf("post: %s", w.Body.String())
		}
	}

	// The local worker reported more recently; that does not matter.
	localCheck := time.Now()
	env.store.UpsertMonitorStatus(ctx, &storage.MonitorStatus{MonitorID: m.ID, Status: "up", ConsecSuccesses: 1, LastCheckAt: &localCheck})
	old := time.Now().Add(-10 * time.Minute)
	post("down", &old)
	if st, _ := env.store.GetMonitorStatus(ctx, m.ID); st == nil || st.Status != "down" {
		t.Errorf("long-cycle result (checked 10 minutes ago, nothing newer from the agent) not applied: %+v", st)
	}
	if inc, err := env.store.GetOpenIncident(ctx, m.ID); err != nil || inc == nil {
		t.Errorf("long-cycle 'down' result opened no incident: %v", err)
	}

	future := time.Now().Add(time.Hour)
	post("down", &future)
	post("up", nil)
	if st, _ := env.store.GetMonitorStatus(ctx, m.ID); st == nil || st.Status != "up" {
		t.Errorf("result after a future checked_at was treated as stale: %+v", st)
	}
}

// Monitors that are never offered to agents (they need credentials) do not
// accept agent results either.
func TestAgentResultsRejectedForServerOnlyMonitor(t *testing.T) {
	env := newTestEnv(t, nil, true, nil)
	ctx := context.Background()
	a, _ := agentMonitor(t, env)
	m := &storage.Monitor{
		Name: "private-api", Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10,
		Enabled: true, AgentEnabled: true, Tags: []string{}, FailureThreshold: 1, SuccessThreshold: 1,
		Settings: json.RawMessage(`{"auth_method":"bearer","bearer_token":"secret"}`),
	}
	if err := env.store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := env.store.SetMonitorAgentIDs(ctx, m.ID, []int64{a.ID}); err != nil {
		t.Fatal(err)
	}
	jobs, _ := env.store.ListAgentJobs(ctx, a.ID)
	for _, j := range jobs {
		if j.ID == m.ID {
			t.Fatal("precondition: monitor with credentials offered to agent")
		}
	}
	w := postResults(t, env, a, `[{"monitor_id":`+strconv.FormatInt(m.ID, 10)+`,"status":"down","message":"forged"}]`)
	if !strings.Contains(w.Body.String(), `"accepted":0`) {
		t.Errorf("result for server-only monitor: %d %s, want accepted 0", w.Code, w.Body.String())
	}
	if inc, err := env.store.GetOpenIncident(ctx, m.ID); err == nil && inc != nil {
		t.Errorf("agent opened incident %d on a server-only monitor", inc.ID)
	}
}

// Guessing the status page password through the API header is throttled by
// the login rate limiter.
func TestStatusPagePasswordHeaderRateLimited(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) {
		headless(c)
		c.Auth.Login.RateLimitPerSec = 0.001
		c.Auth.Login.RateLimitBurst = 2
	}, false, nil)
	sp := env.createStatusPage(t, `{"slug":"locked","title":"Locked","enabled":true,"api_enabled":true,"password":"s3cret"}`)
	path := "/api/v1/status-pages/" + strconv.FormatInt(sp.ID, 10) + "/public"
	wrong := map[string]string{"X-Status-Page-Password": "guess"}

	for i := 0; i < 2; i++ {
		if w := env.do("GET", path, "", wrong); w.Code != http.StatusUnauthorized {
			t.Fatalf("guess %d: %d, want 401", i+1, w.Code)
		}
	}
	if w := env.do("GET", path, "", wrong); w.Code != http.StatusTooManyRequests {
		t.Errorf("guess beyond the login burst: %d, want 429", w.Code)
	}
	if w := env.do("GET", path, "", map[string]string{"X-Status-Page-Password": "s3cret"}); w.Code != http.StatusTooManyRequests {
		t.Errorf("throttled client could still test a password: %d, want 429", w.Code)
	}
}

// Only failed password attempts use up the limiter: a client sending the
// right password is never throttled by its own requests.
func TestStatusPagePasswordHeaderCorrectNotLimited(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) {
		headless(c)
		c.Auth.Login.RateLimitPerSec = 0.001
		c.Auth.Login.RateLimitBurst = 2
	}, false, nil)
	sp := env.createStatusPage(t, `{"slug":"locked","title":"Locked","enabled":true,"api_enabled":true,"password":"s3cret"}`)
	path := "/api/v1/status-pages/" + strconv.FormatInt(sp.ID, 10) + "/public"
	right := map[string]string{"X-Status-Page-Password": "s3cret"}
	wrong := map[string]string{"X-Status-Page-Password": "guess"}

	for i := 0; i < 5; i++ {
		if w := env.do("GET", path, "", right); w.Code != http.StatusOK {
			t.Fatalf("correct password, request %d: %d, want 200", i+1, w.Code)
		}
	}
	// The correct requests left the whole burst for failures.
	for i := 0; i < 2; i++ {
		if w := env.do("GET", path, "", wrong); w.Code != http.StatusUnauthorized {
			t.Fatalf("guess %d after correct requests: %d, want 401", i+1, w.Code)
		}
	}
	if w := env.do("GET", path, "", wrong); w.Code != http.StatusTooManyRequests {
		t.Errorf("guess beyond the burst: %d, want 429", w.Code)
	}
}

// The API header and the web password form share one limiter per client IP,
// so a client cannot get the burst once through each.
func TestStatusPagePasswordLimiterSharedWithWebForm(t *testing.T) {
	limited := func(t *testing.T) (*testEnv, string, string) {
		env := newTestEnv(t, func(c *config.Config) {
			c.Auth.Login.RateLimitPerSec = 0.001
			c.Auth.Login.RateLimitBurst = 2
		}, false, nil)
		sp := env.createStatusPage(t, `{"slug":"locked","title":"Locked","enabled":true,"api_enabled":true,"password":"s3cret"}`)
		return env, "/api/v1/status-pages/" + strconv.FormatInt(sp.ID, 10) + "/public", "/locked/auth"
	}
	form := func(env *testEnv, path, password string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader("password="+password))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		env.srv.ServeHTTP(w, req)
		return w
	}
	wrong := map[string]string{"X-Status-Page-Password": "guess"}

	t.Run("web form then API", func(t *testing.T) {
		env, apiPath, formPath := limited(t)
		for i := 0; i < 2; i++ {
			if w := form(env, formPath, "guess"); w.Code != http.StatusSeeOther {
				t.Fatalf("form guess %d: %d, want 303", i+1, w.Code)
			}
		}
		if w := env.do("GET", apiPath, "", wrong); w.Code != http.StatusTooManyRequests {
			t.Errorf("API guess after the burst was used by the web form: %d, want 429", w.Code)
		}
	})
	t.Run("API then web form", func(t *testing.T) {
		env, apiPath, formPath := limited(t)
		for i := 0; i < 2; i++ {
			if w := env.do("GET", apiPath, "", wrong); w.Code != http.StatusUnauthorized {
				t.Fatalf("API guess %d: %d, want 401", i+1, w.Code)
			}
		}
		w := form(env, formPath, "s3cret")
		if len(w.Result().Cookies()) != 0 || !strings.Contains(w.Header().Get("Location"), "error=1") {
			t.Errorf("web form accepted a password after the burst was used by the API: %d %v", w.Code, w.Header())
		}
	})
}

// Subscriber contacts require their own explicit scope.
func TestSubscriberListNeedsSubscriberPermission(t *testing.T) {
	const roKey = "regression-read-only-key"
	env := newTestEnv(t, func(c *config.Config) {
		c.Auth.APIKeys = append(c.Auth.APIKeys, config.APIKeyConfig{Name: "ro", Hash: config.HashAPIKey(roKey), Permissions: []string{"monitors.read"}})
	}, false, nil)
	ctx := context.Background()
	sp := env.createStatusPage(t, `{"slug":"s1","title":"S1","enabled":true,"api_enabled":true}`)
	for _, sub := range []*storage.StatusPageSubscriber{
		{StatusPageID: sp.ID, Type: "email", Email: "alice@example.com", Confirmed: true},
		{StatusPageID: sp.ID, Type: "webhook", WebhookURL: "https://hooks.example.com/T0/secret-token", Confirmed: true},
	} {
		if err := env.store.CreateStatusPageSubscriber(ctx, sub); err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/v1/status-pages/" + strconv.FormatInt(sp.ID, 10) + "/subscribers"

	w := env.do("GET", path, "", map[string]string{"X-API-Key": roKey})
	if w.Code != http.StatusForbidden {
		t.Fatalf("monitor read-only key: %d %s, want 403", w.Code, w.Body.String())
	}
	if body := w.Body.String(); strings.Contains(body, "alice@example.com") || strings.Contains(body, "secret-token") || strings.Contains(body, `"data"`) {
		t.Errorf("read-only key sees subscriber details: %s", body)
	}

	w = env.do("GET", path, "", admin)
	if body := w.Body.String(); !strings.Contains(body, "alice@example.com") || !strings.Contains(body, `"data"`) {
		t.Errorf("admin lost the subscriber list: %s", body)
	}
}

// A plain portable configuration export includes secrets; redaction is opt-in.
func TestExportIncludesSecretsByDefault(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	ctx := context.Background()
	ch := &storage.NotificationChannel{Name: "hook", Type: "webhook", Enabled: true,
		Settings: json.RawMessage(`{"url":"https://hooks.example.com/secret-path"}`), Events: []string{"incident.created"}}
	if err := env.store.CreateNotificationChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if w := env.do("GET", "/api/v1/export", "", admin); !strings.Contains(w.Body.String(), "secret-path") {
		t.Errorf("default export dropped channel settings: %s", w.Body.String())
	}
	if w := env.do("GET", "/api/v1/export?redact_secrets=true", "", admin); strings.Contains(w.Body.String(), "secret-path") {
		t.Errorf("redact_secrets=true kept channel settings: %s", w.Body.String())
	}
}

// Agent assignments survive export and import by agent name, and a monitor
// whose agents are missing is not widened to every agent.
func TestExportImportKeepsAgentAssignment(t *testing.T) {
	src := newTestEnv(t, nil, false, nil)
	ctx := context.Background()
	a, m := agentMonitor(t, src)
	if err := src.store.SetMonitorAgentIDs(ctx, m.ID, []int64{a.ID}); err != nil {
		t.Fatal(err)
	}
	w := src.do("GET", "/api/v1/export", "", admin)
	if w.Code != http.StatusOK {
		t.Fatalf("export: %d %s", w.Code, w.Body.String())
	}
	exported := w.Body.String()

	importInto := func(env *testEnv) (*storage.Monitor, []int64, string) {
		t.Helper()
		w := env.do("POST", "/api/v1/import?mode=merge", exported, admin)
		if w.Code != http.StatusOK {
			t.Fatalf("import: %d %s", w.Code, w.Body.String())
		}
		res, err := env.store.ListMonitors(ctx, storage.MonitorListFilter{}, storage.Pagination{Page: 1, PerPage: 10})
		if err != nil {
			t.Fatal(err)
		}
		mons := res.Data.([]*storage.Monitor)
		if len(mons) != 1 {
			t.Fatalf("imported %d monitors, want 1", len(mons))
		}
		ids, _ := env.store.GetMonitorAgentIDs(ctx, mons[0].ID)
		return mons[0], ids, w.Body.String()
	}

	// Same agent name on the target instance (with another ID).
	dst := newTestEnv(t, nil, false, nil)
	dst.store.CreateAgent(ctx, &storage.Agent{Name: "unrelated", Enabled: true})
	probe := &storage.Agent{Name: a.Name, Enabled: true}
	if err := dst.store.CreateAgent(ctx, probe); err != nil {
		t.Fatal(err)
	}
	mon, ids, _ := importInto(dst)
	if !mon.AgentEnabled || len(ids) != 1 || ids[0] != probe.ID {
		t.Errorf("imported monitor: agent_enabled=%v agents=%v, want true [%d]", mon.AgentEnabled, ids, probe.ID)
	}

	// No such agent: reject the entire import without granting any jobs.
	bare := newTestEnv(t, nil, false, nil)
	bare.store.CreateAgent(ctx, &storage.Agent{Name: "unrelated", Enabled: true})
	w = bare.do("POST", "/api/v1/import?mode=merge", exported, admin)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "explicit mapping") {
		t.Fatalf("missing destination enrollment must fail atomically: %d %s", w.Code, w.Body.String())
	}
	snapshot, err := bare.store.SnapshotConfiguration(ctx)
	if err != nil || len(snapshot.Monitors)+len(snapshot.Groups)+len(snapshot.Channels) != 0 {
		t.Fatalf("missing enrollment left a partial import: %v", err)
	}
}

// failingAgentStore injects failure at the complete monitor/assignment boundary.
// Storage tests separately prove rollback after intermediate SQL writes.
type failingAgentStore struct{ storage.Store }

func (failingAgentStore) CreateMonitorWithRelations(context.Context, *storage.Monitor, storage.MonitorCreateOptions, ...storage.MonitorAccess) error {
	return errors.New("disk full")
}
func (failingAgentStore) UpdateMonitorWithRelations(context.Context, int64, storage.MonitorUpdate, ...storage.MonitorAccess) (*storage.Monitor, error) {
	return nil, errors.New("disk full")
}
func (failingAgentStore) CloneMonitorWithRelations(context.Context, int64, storage.MonitorClone, []storage.MonitorAccess, []storage.MonitorAccess) (*storage.Monitor, *storage.Heartbeat, error) {
	return nil, nil, errors.New("disk full")
}

func TestMonitorAgentAssignmentFailureIsAtomic(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	ctx := context.Background()
	assigned := &storage.Agent{Name: "assigned", Enabled: true}
	if err := env.store.CreateAgent(ctx, assigned); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Defaults()
	cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "admin", Hash: config.HashAPIKey(testAdminKey), SuperAdmin: true}}
	srv := server.NewServer(cfg, failingAgentStore{env.store}, nil, notifier.NewDispatcher(env.store, logger, true), nil, logger, "test")
	t.Cleanup(srv.Close)
	failing := &testEnv{srv: srv, store: env.store}
	source := &storage.Monitor{Name: "original", Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10, Enabled: true, AgentEnabled: true, FailureThreshold: 1, SuccessThreshold: 1}
	if err := env.store.CreateMonitor(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := env.store.SetMonitorAgentIDs(ctx, source.ID, []int64{assigned.ID}); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"name":"replacement","type":"http","target":"https://example.com","interval":60,"timeout":10,"failure_threshold":1,"success_threshold":1,"agent_enabled":true,"agent_ids":[%d]}`, assigned.ID)
	base := "/api/v1/monitors/" + strconv.FormatInt(source.ID, 10)
	for _, request := range []struct{ method, path, body string }{{"POST", "/api/v1/monitors", body}, {"PUT", base, body}, {"POST", base + "/clone", ""}} {
		w := failing.do(request.method, request.path, request.body, admin)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("%s %s falsely succeeded: %d %s", request.method, request.path, w.Code, w.Body.String())
		}
		stored, err := env.store.GetMonitor(ctx, source.ID)
		if err != nil || stored.Name != "original" || !stored.Enabled || !stored.AgentEnabled {
			t.Fatalf("failed write changed original: %+v, %v", stored, err)
		}
		ids, err := env.store.GetMonitorAgentIDs(ctx, source.ID)
		if err != nil || len(ids) != 1 || ids[0] != assigned.ID {
			t.Fatalf("failed write changed delegation: %v, %v", ids, err)
		}
		result, err := env.store.ListMonitors(ctx, storage.MonitorListFilter{}, storage.Pagination{Page: 1, PerPage: 100})
		if err != nil || result.Total != 1 {
			t.Fatalf("failed write left partial monitor: %+v, %v", result, err)
		}
	}
	w := env.do("POST", base+"/clone", "", admin)
	if w.Code != http.StatusCreated {
		t.Fatalf("working clone: %d %s", w.Code, w.Body.String())
	}
	var clone storage.Monitor
	if err := json.Unmarshal(w.Body.Bytes(), &clone); err != nil {
		t.Fatal(err)
	}
	if !clone.AgentEnabled || len(clone.AgentIDs) != 1 || clone.AgentIDs[0] != assigned.ID {
		t.Fatalf("working clone lost explicit delegation: %+v", clone)
	}
}

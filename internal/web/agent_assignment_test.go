package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/storage"
)

// The monitor form's agent_ids[] field sets, replaces and clears the agent
// assignment, and cloning a monitor copies it.
func TestMonitorFormAgentAssignment(t *testing.T) {
	h, store := storeHandler(t)
	ctx := context.Background()
	a1 := &storage.Agent{Name: "eu", Enabled: true}
	a2 := &storage.Agent{Name: "us", Enabled: true}
	for _, a := range []*storage.Agent{a1, a2} {
		if err := store.CreateAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	assigned := func(monitorID int64) []int64 {
		t.Helper()
		ids, err := store.GetMonitorAgentIDs(ctx, monitorID)
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}
	only := func(ids []int64, want int64) bool { return len(ids) == 1 && ids[0] == want }

	w := httptest.NewRecorder()
	h.MonitorCreate(w, adminRequest("POST", "/monitors", httpMonitorForm("tok", url.Values{"agent_ids[]": {strconvI(a1.ID)}})))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("create returned %d: %s", w.Code, w.Body.String())
	}
	res, err := store.ListMonitors(ctx, storage.MonitorListFilter{}, storage.Pagination{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	mons := res.Data.([]*storage.Monitor)
	if len(mons) != 1 {
		t.Fatalf("created %d monitors, want 1", len(mons))
	}
	mon := mons[0]
	if got := assigned(mon.ID); !only(got, a1.ID) {
		t.Fatalf("create: agents %v, want [%d]", got, a1.ID)
	}

	id := strconvI(mon.ID)
	update := func(extra url.Values) {
		t.Helper()
		w := httptest.NewRecorder()
		r := adminRequest("POST", "/monitors/"+id, httpMonitorForm("", extra))
		r.SetPathValue("id", id)
		h.MonitorUpdate(w, r)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("update returned %d: %s", w.Code, w.Body.String())
		}
	}
	update(url.Values{"agent_ids[]": {strconvI(a2.ID)}})
	if got := assigned(mon.ID); !only(got, a2.ID) {
		t.Fatalf("update: agents %v, want [%d]", got, a2.ID)
	}

	w = httptest.NewRecorder()
	r := adminRequest("POST", "/monitors/"+id+"/clone", nil)
	r.SetPathValue("id", id)
	h.MonitorClone(w, r)
	res, err = store.ListMonitors(ctx, storage.MonitorListFilter{}, storage.Pagination{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	var clone *storage.Monitor
	for _, m := range res.Data.([]*storage.Monitor) {
		if m.ID != mon.ID {
			clone = m
		}
	}
	if clone == nil {
		t.Fatalf("clone not created (%d)", w.Code)
	}
	if got := assigned(clone.ID); !only(got, a2.ID) {
		t.Errorf("clone: agents %v, want [%d]", got, a2.ID)
	}

	// No agent_ids[] in the form clears the assignment.
	update(nil)
	if got := assigned(mon.ID); len(got) != 0 {
		t.Errorf("update without agent_ids[]: agents %v, want none", got)
	}
}

// failingAgentStore fails every attempt to store an agent assignment.
type failingAgentStore struct {
	*storage.SQLiteStore
}

func (failingAgentStore) SetMonitorAgentIDs(context.Context, int64, []int64) error {
	return errors.New("disk full")
}

// If storing the agent assignment of a new monitor fails, the monitor must
// not run on every agent: it is left with agent checks off and the user is
// told so.
func TestMonitorCreateAgentAssignmentFailure(t *testing.T) {
	h, store := storeHandler(t)
	ctx := context.Background()
	a := &storage.Agent{Name: "eu", Enabled: true}
	if err := store.CreateAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	h.store = failingAgentStore{store}

	w := httptest.NewRecorder()
	h.MonitorCreate(w, adminRequest("POST", "/monitors", httpMonitorForm("", url.Values{"agent_enabled": {"on"}, "agent_ids[]": {strconvI(a.ID)}})))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("create returned %d: %s", w.Code, w.Body.String())
	}
	res, err := store.ListMonitors(ctx, storage.MonitorListFilter{}, storage.Pagination{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	mons := res.Data.([]*storage.Monitor)
	if len(mons) != 1 {
		t.Fatalf("created %d monitors, want 1", len(mons))
	}
	if mons[0].AgentEnabled {
		t.Error("monitor whose agent assignment failed has agent checks on, so it runs on every agent")
	}
	if jobs, _ := store.ListAgentJobs(ctx, a.ID); len(jobs) != 0 {
		t.Errorf("agent %d is offered %d job(s), want none", a.ID, len(jobs))
	}
	if c := w.Result().Cookies(); len(c) == 0 || !strings.Contains(c[0].Value, "error") {
		t.Errorf("no error shown to the user: cookies %v", c)
	}
}

// With a working store, the assigned monitor is created with agent checks on.
func TestMonitorCreateAgentAssignmentKeepsAgentEnabled(t *testing.T) {
	h, store := storeHandler(t)
	ctx := context.Background()
	a := &storage.Agent{Name: "eu", Enabled: true}
	if err := store.CreateAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.MonitorCreate(w, adminRequest("POST", "/monitors", httpMonitorForm("", url.Values{"agent_enabled": {"on"}, "agent_ids[]": {strconvI(a.ID)}})))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("create returned %d: %s", w.Code, w.Body.String())
	}
	if jobs, err := store.ListAgentJobs(ctx, a.ID); err != nil || len(jobs) != 1 {
		t.Errorf("assigned agent's jobs = %v, %v; want the new monitor with agent checks on", jobs, err)
	}
}

// errorToast reports whether the response sets an error toast.
func errorToast(w *httptest.ResponseRecorder) bool {
	c := w.Result().Cookies()
	return len(c) > 0 && strings.Contains(c[0].Value, "error")
}

// runsOn reports whether agent is offered monitor id.
func runsOn(t *testing.T, store *storage.SQLiteStore, agent, id int64) bool {
	t.Helper()
	jobs, err := store.ListAgentJobs(context.Background(), agent)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.ID == id {
			return true
		}
	}
	return false
}

// If storing the new agent assignment of an edited monitor fails, the monitor
// must not keep running on every agent: agent checks are switched off and the
// user is shown an error.
func TestMonitorUpdateAgentAssignmentFailure(t *testing.T) {
	h, store := storeHandler(t)
	ctx := context.Background()
	assigned := &storage.Agent{Name: "eu", Enabled: true}
	other := &storage.Agent{Name: "us", Enabled: true}
	for _, a := range []*storage.Agent{assigned, other} {
		if err := store.CreateAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	mon := &storage.Monitor{Name: "API", Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10,
		Enabled: true, AgentEnabled: true, FailureThreshold: 1, SuccessThreshold: 1}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}
	h.store = failingAgentStore{store}

	id := strconvI(mon.ID)
	w := httptest.NewRecorder()
	r := adminRequest("POST", "/monitors/"+id, httpMonitorForm("", url.Values{"agent_enabled": {"on"}, "agent_ids[]": {strconvI(assigned.ID)}}))
	r.SetPathValue("id", id)
	h.MonitorUpdate(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("update returned %d: %s", w.Code, w.Body.String())
	}
	got, err := store.GetMonitor(ctx, mon.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentEnabled || runsOn(t, store, other.ID, mon.ID) {
		t.Error("monitor whose agent assignment failed still runs on every agent")
	}
	if !errorToast(w) {
		t.Errorf("no error shown to the user: cookies %v", w.Result().Cookies())
	}

	// With a working store the assignment is saved and agent checks are on.
	h.store = store
	w = httptest.NewRecorder()
	r = adminRequest("POST", "/monitors/"+id, httpMonitorForm("", url.Values{"agent_enabled": {"on"}, "agent_ids[]": {strconvI(assigned.ID)}}))
	r.SetPathValue("id", id)
	h.MonitorUpdate(w, r)
	if !runsOn(t, store, assigned.ID, mon.ID) || runsOn(t, store, other.ID, mon.ID) {
		t.Error("with a working store the monitor does not run on exactly its assigned agent")
	}
	if errorToast(w) {
		t.Error("successful update shows an error")
	}
}

// If storing the agent assignment of a clone fails, the clone must not run on
// every agent once resumed, and the user is shown an error.
func TestMonitorCloneAgentAssignmentFailure(t *testing.T) {
	h, store := storeHandler(t)
	ctx := context.Background()
	assigned := &storage.Agent{Name: "eu", Enabled: true}
	other := &storage.Agent{Name: "us", Enabled: true}
	for _, a := range []*storage.Agent{assigned, other} {
		if err := store.CreateAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	src := &storage.Monitor{Name: "API", Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10,
		Enabled: true, AgentEnabled: true, FailureThreshold: 1, SuccessThreshold: 1}
	if err := store.CreateMonitor(ctx, src); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMonitorAgentIDs(ctx, src.ID, []int64{assigned.ID}); err != nil {
		t.Fatal(err)
	}
	h.store = failingAgentStore{store}

	id := strconvI(src.ID)
	w := httptest.NewRecorder()
	r := adminRequest("POST", "/monitors/"+id+"/clone", nil)
	r.SetPathValue("id", id)
	h.MonitorClone(w, r)
	res, err := store.ListMonitors(ctx, storage.MonitorListFilter{}, storage.Pagination{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	var clone *storage.Monitor
	for _, m := range res.Data.([]*storage.Monitor) {
		if m.ID != src.ID {
			clone = m
		}
	}
	if clone == nil {
		t.Fatalf("clone not created (%d)", w.Code)
	}
	if err := store.SetMonitorEnabled(ctx, clone.ID, true); err != nil {
		t.Fatal(err)
	}
	if runsOn(t, store, other.ID, clone.ID) {
		t.Error("resumed clone whose agent assignment failed runs on every agent")
	}
	if !errorToast(w) {
		t.Errorf("no error shown to the user: cookies %v", w.Result().Cookies())
	}
}

// A clone keeps the source's agent setting and assignment (v1.3.0 dropped
// agent_enabled on web clones while the API clone kept it).
func TestMonitorCloneKeepsAgentSettings(t *testing.T) {
	h, store := storeHandler(t)
	ctx := context.Background()
	assigned := &storage.Agent{Name: "eu", Enabled: true}
	if err := store.CreateAgent(ctx, assigned); err != nil {
		t.Fatal(err)
	}
	src := &storage.Monitor{Name: "API", Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10,
		Enabled: true, AgentEnabled: true, FailureThreshold: 1, SuccessThreshold: 1}
	if err := store.CreateMonitor(ctx, src); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMonitorAgentIDs(ctx, src.ID, []int64{assigned.ID}); err != nil {
		t.Fatal(err)
	}

	id := strconvI(src.ID)
	r := adminRequest("POST", "/monitors/"+id+"/clone", nil)
	r.SetPathValue("id", id)
	h.MonitorClone(httptest.NewRecorder(), r)

	res, err := store.ListMonitors(ctx, storage.MonitorListFilter{}, storage.Pagination{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.Data.([]*storage.Monitor) {
		if m.ID == src.ID {
			continue
		}
		if !m.AgentEnabled {
			t.Error("clone lost agent_enabled")
		}
		ids, err := store.GetMonitorAgentIDs(ctx, m.ID)
		if err != nil || len(ids) != 1 || ids[0] != assigned.ID {
			t.Errorf("clone agent_ids = %v, %v; want [%d]", ids, err, assigned.ID)
		}
		return
	}
	t.Fatal("clone not created")
}

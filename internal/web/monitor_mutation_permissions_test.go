package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/v2/internal/config"
	"github.com/y0f/asura/v2/internal/httputil"
	"github.com/y0f/asura/v2/internal/storage"
)

func TestHeartbeatCreationRevealsOnlyCommittedURL(t *testing.T) {
	h, store := storeHandler(t)
	h.cfg.Server.BasePath, h.cfg.Server.ExternalURL = "/asura", "https://status.example/asura"
	key := &config.APIKeyConfig{Name: "creator", Permissions: []string{"monitors.create"}}
	r := adminRequest("POST", "/asura/monitors", url.Values{"name": {"job"}, "type": {"heartbeat"}, "interval": {"60"}, "timeout": {"10"}, "failure_threshold": {"1"}, "success_threshold": {"1"}, "settings_mode": {"json"}, "settings_json": {`{"grace":5}`}})
	r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, key))
	w := httptest.NewRecorder()
	h.MonitorCreate(w, r)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "shown once") {
		t.Fatalf("one-time creation response: %d %s", w.Code, w.Body.String())
	}
	result, err := store.ListMonitors(context.Background(), storage.MonitorListFilter{}, storage.Pagination{Page: 1, PerPage: 10})
	if err != nil || result.Total != 1 {
		t.Fatalf("creation did not commit one monitor: %+v, %v", result, err)
	}
	m := result.Data.([]*storage.Monitor)[0]
	heartbeat, err := store.GetHeartbeatByMonitorID(context.Background(), m.ID)
	if err != nil || strings.Contains(w.Body.String(), heartbeat.Token) {
		t.Fatalf("missing heartbeat or stored hash disclosed: %+v, %v", heartbeat, err)
	}
	prefix := "https://status.example/asura/api/v1/heartbeat/"
	_, suffix, ok := strings.Cut(w.Body.String(), prefix)
	if !ok {
		t.Fatal("creation omitted the configured base-path ping URL")
	}
	token, _, _ := strings.Cut(suffix, `"`)
	if stored, err := store.GetHeartbeatByToken(context.Background(), token); err != nil || stored.MonitorID != m.ID {
		t.Fatalf("revealed URL does not authenticate the committed heartbeat: %v", err)
	}
	if strings.Contains(w.Body.String(), `href="/asura/monitors/`+strconvI(m.ID)+`"`) {
		t.Fatal("write-only creation exposed an unauthorized detail link")
	}
	r = adminRequest("GET", "/asura/monitors/"+strconvI(m.ID), nil)
	r.SetPathValue("id", strconvI(m.ID))
	r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, key))
	w = httptest.NewRecorder()
	h.MonitorDetail(w, r)
	if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), token) {
		t.Fatalf("creation authority became monitor read: %d", w.Code)
	}
}

func TestMonitorFormRejectsMalformedRelationAndSLATarget(t *testing.T) {
	for _, value := range []string{"oops", "NaN", "+Inf", "-1", "100.1"} {
		r := buildFormRequest(httpMonitorForm("", url.Values{"sla_target": {value}}))
		mon, _, _ := testWebHandler(t).parseMonitorForm(r)
		if err := validateMonitorForm(r, mon); err == nil {
			t.Errorf("invalid SLA target %q was silently coerced", value)
		}
	}
	for _, field := range []string{"group_id", "proxy_id", "escalation_policy_id", "notification_channel_ids[]", "tag_ids[]", "agent_ids[]"} {
		r := buildFormRequest(url.Values{field: {"not-an-id"}})
		if err := validateMonitorRelationForm(r); err == nil {
			t.Errorf("invalid %s was silently discarded", field)
		}
	}
}

type failingMonitorChoices struct{ storage.Store }

func (failingMonitorChoices) ListTags(context.Context) ([]*storage.Tag, error) {
	return nil, errors.New("injected metadata read failure")
}
func TestMonitorFormDoesNotRenderIncompleteChoices(t *testing.T) {
	h, store := storeHandler(t)
	h.store = failingMonitorChoices{store}
	w := httptest.NewRecorder()
	h.MonitorForm(w, adminRequest("GET", "/monitors/new", nil))
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), `name="relation_fields"`) {
		t.Fatalf("failed metadata query produced a usable replacement form: %d %s", w.Code, w.Body.String())
	}
}

type failingMonitorDetail struct {
	storage.Store
	stage      string
	dailyCalls int
}

func (s *failingMonitorDetail) ListCheckResults(ctx context.Context, id int64, p storage.Pagination, a ...storage.MonitorAccess) (*storage.PaginatedResult, error) {
	if s.stage == "checks" {
		return nil, errors.New("checks failure")
	}
	return s.Store.ListCheckResults(ctx, id, p, a...)
}
func (s *failingMonitorDetail) ListCheckSummaries(ctx context.Context, id int64, p storage.Pagination, a ...storage.MonitorAccess) (*storage.PaginatedResult, error) {
	if s.stage == "checks" {
		return nil, errors.New("check summary failure")
	}
	return s.Store.ListCheckSummaries(ctx, id, p, a...)
}
func (s *failingMonitorDetail) ListContentChanges(ctx context.Context, id int64, p storage.Pagination, a ...storage.MonitorAccess) (*storage.PaginatedResult, error) {
	if s.stage == "changes" {
		return nil, errors.New("changes failure")
	}
	return s.Store.ListContentChanges(ctx, id, p, a...)
}
func (s *failingMonitorDetail) ListContentChangeSummaries(ctx context.Context, id int64, p storage.Pagination, a ...storage.MonitorAccess) (*storage.PaginatedResult, error) {
	if s.stage == "changes" {
		return nil, errors.New("change summary failure")
	}
	return s.Store.ListContentChangeSummaries(ctx, id, p, a...)
}
func (s *failingMonitorDetail) GetMonitorMetricsSnapshot(ctx context.Context, id int64, from, to time.Time, a ...storage.MonitorAccess) (*storage.MonitorMetricsSnapshot, error) {
	if s.stage == "metrics" {
		return nil, errors.New("metrics failure")
	}
	return s.Store.GetMonitorMetricsSnapshot(ctx, id, from, to, a...)
}
func (s *failingMonitorDetail) GetMonitorSLASnapshot(ctx context.Context, id int64, from, to, rollingFrom time.Time, a ...storage.MonitorAccess) (*storage.MonitorSLASnapshot, error) {
	if s.stage == "sla" {
		return nil, errors.New("SLA failure")
	}
	return s.Store.GetMonitorSLASnapshot(ctx, id, from, to, rollingFrom, a...)
}
func (s *failingMonitorDetail) GetLatestCheckResult(ctx context.Context, id int64, a ...storage.MonitorAccess) (*storage.CheckResult, error) {
	if s.stage == "latest" {
		return nil, errors.New("latest failure")
	}
	return s.Store.GetLatestCheckResult(ctx, id, a...)
}
func (s *failingMonitorDetail) GetLatestCheckSummary(ctx context.Context, id int64, a ...storage.MonitorAccess) (*storage.CheckResult, error) {
	if s.stage == "latest" {
		return nil, errors.New("latest summary failure")
	}
	return s.Store.GetLatestCheckSummary(ctx, id, a...)
}
func (s *failingMonitorDetail) GetMonitorTags(ctx context.Context, id int64, a ...storage.MonitorAccess) ([]storage.MonitorTag, error) {
	if s.stage == "tags" {
		return nil, errors.New("tags failure")
	}
	return s.Store.GetMonitorTags(ctx, id, a...)
}
func (s *failingMonitorDetail) GetDailyUptime(ctx context.Context, id int64, from, to time.Time, a ...storage.MonitorAccess) ([]*storage.DailyUptime, error) {
	s.dailyCalls++
	if s.stage == "daily" || (s.stage == "heatmap" && s.dailyCalls == 3) {
		return nil, errors.New("uptime failure")
	}
	return s.Store.GetDailyUptime(ctx, id, from, to, a...)
}
func TestMonitorDetailReportsReadFailures(t *testing.T) {
	for _, stage := range []string{"checks", "changes", "metrics", "latest", "tags", "daily", "heatmap", "sla"} {
		t.Run(stage, func(t *testing.T) {
			h, store := storeHandler(t)
			m := &storage.Monitor{Name: "monitor", Type: "http", Target: "https://example.test", Enabled: true, Interval: 60, Timeout: 10}
			if stage == "sla" {
				m.SLATarget = 99
			}
			if err := store.CreateMonitor(context.Background(), m); err != nil {
				t.Fatal(err)
			}
			h.store = &failingMonitorDetail{Store: store, stage: stage}
			r := adminRequest("GET", "/monitors/"+strconvI(m.ID), nil)
			r.SetPathValue("id", strconvI(m.ID))
			w := httptest.NewRecorder()
			h.MonitorDetail(w, r)
			if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "100.00%") {
				t.Fatalf("%s failure became successful empty detail: HTTP %d", stage, w.Code)
			}
		})
	}
}

func TestMonitorDetailReportsSummaryReadFailures(t *testing.T) {
	for _, stage := range []string{"checks", "changes", "latest"} {
		t.Run(stage, func(t *testing.T) {
			h, store := storeHandler(t)
			m := &storage.Monitor{Name: "monitor", Type: "http", Target: "https://example.test", Enabled: true, Interval: 60, Timeout: 10}
			if err := store.CreateMonitor(context.Background(), m); err != nil {
				t.Fatal(err)
			}
			h.store = &failingMonitorDetail{Store: store, stage: stage}
			r := adminRequest("GET", "/monitors/"+strconvI(m.ID), nil)
			r.SetPathValue("id", strconvI(m.ID))
			r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, &config.APIKeyConfig{Permissions: []string{"monitors.read"}}))
			w := httptest.NewRecorder()
			h.MonitorDetail(w, r)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("%s summary failure became successful empty detail: HTTP%d", stage, w.Code)
			}
		})
	}
}

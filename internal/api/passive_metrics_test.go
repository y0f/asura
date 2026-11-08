package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/storage"
)

func TestPassiveMetricsAPIIsUnavailableAndPreservesLegacyTarget(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	ctx := context.Background()
	m := &storage.Monitor{Name: "manual", Type: "manual", Target: "manual", Enabled: true, Interval: 60, Timeout: 10, SLATarget: 99.9}
	if err := env.store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.RecordManualStatus(ctx, m.ID, "up", "status update"); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/monitors/%d", m.ID)
	w := env.do("GET", path+"/metrics", "", admin)
	var result map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result["sampled_metrics"] != false || result["uptime_pct"] != nil || result["response_time"] != nil || result["checks"] != nil {
		t.Fatalf("passive metric response: %d %s", w.Code, w.Body.String())
	}
	if w := env.do("GET", path+"/sla", "", admin); w.Code != http.StatusBadRequest {
		t.Fatalf("passive SLA: %d %s", w.Code, w.Body.String())
	}
	if w := env.do("PUT", path, `{"name":"renamed","type":"manual","target":"manual","interval":60,"timeout":10,"failure_threshold":1,"success_threshold":1,"sla_target":99.9}`, admin); w.Code != 200 {
		t.Fatalf("legacy metadata edit: %d %s", w.Code, w.Body.String())
	}
	stored, err := env.store.GetMonitor(ctx, m.ID)
	if err != nil || stored.SLATarget != m.SLATarget {
		t.Fatalf("legacy inactive target lost: %v", err)
	}
	if w := env.do("PUT", path, `{"name":"renamed","type":"manual","target":"manual","interval":60,"timeout":10,"failure_threshold":1,"success_threshold":1,"sla_target":98}`, admin); w.Code != http.StatusBadRequest {
		t.Fatalf("new passive target accepted: %d %s", w.Code, w.Body.String())
	}
	if w := env.do("POST", "/api/v1/monitors", `{"name":"new passive","type":"manual","sla_target":99.9}`, admin); w.Code != http.StatusBadRequest {
		t.Fatalf("new passive SLA accepted: %d %s", w.Code, w.Body.String())
	}
	sp := &storage.StatusPage{Slug: "passive", Title: "State", Enabled: true}
	if err := env.store.CreateStatusPage(ctx, sp); err != nil {
		t.Fatal(err)
	}
	if err := env.store.SetStatusPageMonitors(ctx, sp.ID, []storage.StatusPageMonitor{{MonitorID: m.ID}}); err != nil {
		t.Fatal(err)
	}
	if w := env.do("GET", fmt.Sprintf("/api/v1/status-pages/%d/public", sp.ID), "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"uptime_90d":null`) {
		t.Fatalf("public state invented uptime: %d %s", w.Code, w.Body.String())
	}
	if w := env.do("GET", fmt.Sprintf("/api/v1/badge/%d/uptime", m.ID), "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "n/a") {
		t.Fatalf("passive badge: %d %s", w.Code, w.Body.String())
	}
	for _, query := range []string{"?from=bad", "?to=bad", "?from=2026-01-02T00:00:00Z&to=2026-01-01T00:00:00Z"} {
		if w := env.do("GET", path+"/metrics"+query, "", admin); w.Code != 400 {
			t.Fatalf("invalid metric window accepted: %d", w.Code)
		}
	}
}

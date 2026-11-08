package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/y0f/asura/internal/storage"
)

func TestPublicStatusNoSamplesAreNullNotPerfectUptimeAPI(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	ctx := context.Background()
	m := &storage.Monitor{Name: "waiting", Type: "http", Target: "https://private.example.test", Enabled: true, Interval: 60, Timeout: 10}
	if err := env.store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	sp := &storage.StatusPage{Title: "Services", Slug: "services", Enabled: true}
	if err := env.store.CreateStatusPage(ctx, sp); err != nil {
		t.Fatal(err)
	}
	if err := env.store.SetStatusPageMonitors(ctx, sp.ID, []storage.StatusPageMonitor{{MonitorID: m.ID}}); err != nil {
		t.Fatal(err)
	}
	w := env.do("GET", fmt.Sprintf("/api/v1/status-pages/%d/public", sp.ID), "", nil)
	var response struct {
		Overall  string `json:"overall_status"`
		Monitors []struct {
			Uptime *float64 `json:"uptime_90d"`
			Total  int64    `json:"total_checks"`
		} `json:"monitors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || response.Overall != "unknown" || len(response.Monitors) != 1 || response.Monitors[0].Uptime != nil || response.Monitors[0].Total != 0 {
		t.Fatalf("unmeasured uptime presented as healthy: %d %s", w.Code, w.Body.String())
	}
}

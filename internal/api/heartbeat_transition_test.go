package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/y0f/asura/v3/internal/storage"
)

func TestHeartbeatPingKeepsDashboardCurrent(t *testing.T) {
	for _, withPipeline := range []bool{false, true} {
		t.Run(fmt.Sprintf("pipeline=%v", withPipeline), func(t *testing.T) {
			env := newTestEnv(t, nil, withPipeline, nil)
			ctx := context.Background()
			w := env.do("POST", "/api/v1/monitors", `{"name":"cron","type":"heartbeat","interval":60,"timeout":10}`, admin)
			if w.Code != http.StatusCreated {
				t.Fatalf("create: %d %s", w.Code, w.Body.String())
			}
			var created struct {
				Monitor   storage.Monitor   `json:"monitor"`
				Heartbeat storage.Heartbeat `json:"heartbeat"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			var incidentID int64
			if withPipeline {
				inc := &storage.Incident{MonitorID: created.Monitor.ID, Status: "open", Severity: "critical", Cause: "missed heartbeat"}
				if err := env.store.CreateIncident(ctx, inc); err != nil {
					t.Fatal(err)
				}
				incidentID = inc.ID
			}
			for _, method := range []string{"GET", "POST"} {
				old := time.Now().Add(-time.Hour)
				if err := env.store.UpsertMonitorStatus(ctx, &storage.MonitorStatus{MonitorID: created.Monitor.ID, Status: "pending", LastCheckAt: &old}); err != nil {
					t.Fatal(err)
				}
				w := env.do(method, "/api/v1/heartbeat/"+created.Heartbeat.Token, "", nil)
				if w.Code != http.StatusOK {
					t.Fatalf("ping: %d %s", w.Code, w.Body.String())
				}
				status, err := env.store.GetMonitorStatus(ctx, created.Monitor.ID)
				if err != nil || status.Status != "up" || status.LastCheckAt == nil || !status.LastCheckAt.After(old) {
					t.Fatalf("successful ping left stale dashboard: %+v %v", status, err)
				}
			}
			if withPipeline {
				inc, err := env.store.GetIncident(ctx, incidentID)
				if err != nil || inc.Status != "resolved" {
					t.Fatalf("ping did not recover stored incident: %+v %v", inc, err)
				}
			}
		})
	}
}

func TestManualStatusReportsPersistenceFailure(t *testing.T) {
	for _, withPipeline := range []bool{false, true} {
		t.Run(fmt.Sprintf("pipeline=%v", withPipeline), func(t *testing.T) {
			env := newTestEnv(t, nil, withPipeline, nil)
			mon := &storage.Monitor{Name: "manual", Type: "manual", Target: "manual", Interval: 60, Timeout: 10, Enabled: true, FailureThreshold: 1, SuccessThreshold: 1}
			if err := env.store.CreateMonitor(context.Background(), mon); err != nil {
				t.Fatal(err)
			}
			fixture, err := sql.Open("sqlite", env.dbPath)
			if err != nil {
				t.Fatal(err)
			}
			_, err = fixture.Exec(`CREATE TRIGGER reject_manual_check BEFORE INSERT ON check_results BEGIN SELECT RAISE(ABORT, 'injected failure'); END`)
			fixture.Close()
			if err != nil {
				t.Fatal(err)
			}
			w := env.do("POST", fmt.Sprintf("/api/v1/monitors/%d/status", mon.ID), `{"status":"down"}`, admin)
			want := http.StatusServiceUnavailable
			if withPipeline {
				want = http.StatusInternalServerError
			}
			if w.Code != want {
				t.Fatalf("failed status change returned %d, want %d: %s", w.Code, want, w.Body.String())
			}
		})
	}
}

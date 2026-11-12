package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/v3/internal/storage"
)

type passiveNoMetricsStore struct{ storage.Store }

func (s passiveNoMetricsStore) GetMonitorMetricsSnapshot(context.Context, int64, time.Time, time.Time, ...storage.MonitorAccess) (*storage.MonitorMetricsSnapshot, error) {
	return nil, errors.New("passive monitor requested sample metrics")
}
func (s passiveNoMetricsStore) GetDailyUptime(context.Context, int64, time.Time, time.Time, ...storage.MonitorAccess) ([]*storage.DailyUptime, error) {
	return nil, errors.New("passive monitor requested uptime")
}

func TestPassiveDetailUsesReportedStateWithoutSampleQueries(t *testing.T) {
	for _, kind := range []string{"manual", "heartbeat"} {
		t.Run(kind, func(t *testing.T) {
			h, s := storeHandler(t)
			ctx := context.Background()
			m := &storage.Monitor{Name: "state", Type: kind, Target: kind, Interval: 60, Timeout: 10, Enabled: true, SLATarget: 99.9}
			if err := s.CreateMonitor(ctx, m); err != nil {
				t.Fatal(err)
			}
			secret := "synthetic-private-ping-token"
			if kind == "heartbeat" {
				if err := s.CreateHeartbeat(ctx, &storage.Heartbeat{MonitorID: m.ID, Token: secret, Grace: 27, Status: "pending"}); err != nil {
					t.Fatal(err)
				}
				if err := s.UpdateHeartbeatPing(ctx, m.ID, secret); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := s.RecordManualStatus(ctx, m.ID, "down", "state changed"); err != nil {
					t.Fatal(err)
				}
			}
			h.store = passiveNoMetricsStore{s}
			r := adminRequest("GET", "/monitors/"+strconvI(m.ID), nil)
			r.SetPathValue("id", strconvI(m.ID))
			w := httptest.NewRecorder()
			h.MonitorDetail(w, r)
			body := w.Body.String()
			if w.Code != 200 || strings.Contains(body, secret) || strings.Contains(body, "percentile-summary") || strings.Contains(body, "uptime-heatmap") {
				t.Fatalf("passive detail failed safe state projection: HTTP%d", w.Code)
			}
			if kind == "heartbeat" && (!strings.Contains(body, "27 seconds") || strings.Contains(body, "No ping received")) {
				t.Fatal("heartbeat facts missing")
			}
			if kind == "manual" && !strings.Contains(body, "Recorded state history") {
				t.Fatal("manual history missing")
			}
		})
	}
}

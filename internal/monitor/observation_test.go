package monitor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/y0f/asura/internal/checker"
	"github.com/y0f/asura/internal/incident"
	"github.com/y0f/asura/internal/storage"
)

func TestObservationStartsProbeComparisonsWithoutPassiveCaches(t *testing.T) {
	for _, kind := range []string{storage.ObservationUnknown, storage.ObservationManual, storage.ObservationHeartbeat, storage.ObservationProbe} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			s := &checkFailureStore{SQLiteStore: testStore(t)}
			logger := discardLogger()
			p := NewPipeline(s, checker.NewRegistry(), incident.NewManager(s, logger), 1, false, logger)
			m := createTestMonitor(t, s, &storage.Monitor{})
			now := time.Now()
			before := &storage.MonitorStatus{MonitorID: m.ID, ConfigRevision: m.ConfigRevision,
				Status: "up", ObservationKind: kind, LastBodyHash: "old body", LastCertFingerprint: "old cert",
				BaselineAvg: 100, BaselineStddev: 5, BaselineUpdatedAt: &now}
			if err := s.UpsertMonitorStatus(ctx, before); err != nil {
				t.Fatal(err)
			}
			if err := s.UpdateBaseline(ctx, m.ID, 100, 5); err != nil {
				t.Fatal(err)
			}
			staged, err := p.loadMonitorStatus(ctx, m)
			if err != nil {
				t.Fatal(err)
			}
			if kind == storage.ObservationProbe {
				if staged.LastBodyHash != before.LastBodyHash || staged.LastCertFingerprint != before.LastCertFingerprint || staged.BaselineAvg != 100 {
					t.Fatal("genuine prior probe comparisons were discarded")
				}
			} else if staged.LastBodyHash != "" || staged.LastCertFingerprint != "" || staged.BaselineAvg != 0 || staged.BaselineStddev != 0 || staged.BaselineUpdatedAt != nil {
				t.Fatalf("unproven cache seeds a new probe: %+v", staged)
			}
			s.writeError = errors.New("injected check commit failure")
			p.handleResult(ctx, WorkerResult{Monitor: m, Result: &checker.Result{Status: "up", CertFingerprint: "new cert"}})
			persisted, err := s.GetMonitorStatus(ctx, m.ID)
			if err != nil || persisted.ObservationKind != kind || persisted.LastBodyHash != before.LastBodyHash || persisted.LastCertFingerprint != before.LastCertFingerprint || persisted.BaselineAvg != 100 {
				t.Fatalf("failed result committed staged cache reset: %+v %v", persisted, err)
			}
			if events := drainEvents(p); len(events) != 0 {
				t.Fatalf("failed result published comparison: %v", events)
			}
			s.writeError = nil
			p.handleResult(ctx, WorkerResult{Monitor: m, Result: &checker.Result{Status: "up", CertFingerprint: "new cert"}})
			persisted, err = s.GetMonitorStatus(ctx, m.ID)
			if err != nil || persisted.ObservationKind != storage.ObservationProbe || persisted.LastCertFingerprint != "new cert" {
				t.Fatalf("new probe provenance not persisted: %+v %v", persisted, err)
			}
			if kind != storage.ObservationProbe && (persisted.BaselineAvg != 0 || persisted.BaselineStddev != 0 || persisted.BaselineUpdatedAt != nil) {
				t.Fatalf("next probe would reuse stale baseline: %+v", persisted)
			}
			events := drainEvents(p)
			wantChanges := 0
			if kind == storage.ObservationProbe {
				wantChanges = 1
			}
			if got := countEvents(events, "cert.changed"); got != wantChanges {
				t.Fatalf("certificate comparison from %s: %d want%d", kind, got, wantChanges)
			}
		})
	}
}

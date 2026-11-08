package storage

import (
	"context"
	"testing"
)

func TestMonitorStatusCountsKeepPendingSeparate(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, status := range []string{"pending", "up", "down", "degraded", "paused", "invalid-legacy"} {
		m := newTestMonitor(t, s, status)
		if err := s.UpsertMonitorStatus(ctx, &MonitorStatus{MonitorID: m.ID, Status: status}); err != nil {
			t.Fatal(err)
		}
		if status == "paused" {
			m.Enabled = false
			if err := s.UpdateMonitor(ctx, m); err != nil {
				t.Fatal(err)
			}
		}
	}
	up, down, degraded, paused, pending, err := s.CountMonitorsByStatus(ctx)
	if err != nil || up != 1 || down != 1 || degraded != 1 || paused != 1 || pending != 2 {
		t.Fatalf("counts = %d/%d/%d/%d/%d: %v", up, down, degraded, paused, pending, err)
	}
	up, down, degraded, paused, pending, err = s.CountMonitorsByStatus(ctx, MonitorAccess{})
	if err != nil || up+down+degraded+paused+pending != 0 {
		t.Fatalf("denied scope counted monitors: %v", err)
	}
}

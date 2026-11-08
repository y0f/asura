package storage

import (
	"context"
	"reflect"
	"testing"
)

func TestSLAMonitorScanUsesEnabledTargetsAndKeyset(t *testing.T) {
	s, _, monitors := accessFixture(t)
	ctx := context.Background()
	for i, m := range monitors {
		m.SLATarget = 99.9
		if i == 2 {
			m.SLATarget = 0
		}
		if i == 4 {
			m.Enabled = false
		}
		if err := s.UpdateMonitor(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	var got []int64
	var cursor int64
	for {
		ids, err := s.ListSLAMonitorIDs(ctx, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) == 0 {
			break
		}
		got = append(got, ids...)
		cursor = ids[len(ids)-1]
	}
	want := []int64{monitors[0].ID, monitors[1].ID, monitors[3].ID, monitors[5].ID}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("eligible SLA IDs=%v want=%v", got, want)
	}
	for _, limit := range []int{-1, 0, 1001} {
		if _, err := s.ListSLAMonitorIDs(ctx, 0, limit); err == nil {
			t.Errorf("unbounded batch accepted limit=%d", limit)
		}
	}
}

package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func relationErrorMonitor() *Monitor {
	return &Monitor{Name: "fixture", Type: "http", Target: "https://example.test", Interval: 60, Timeout: 10, Enabled: true}
}

func TestMonitorRelationErrorsPreserveRollbackAndClassification(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"proxy", "policy", "channel", "tag", "agent"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			m := relationErrorMonitor()
			missing := int64(9999)
			switch kind {
			case "proxy":
				m.ProxyID = &missing
			case "policy":
				m.EscalationPolicyID = &missing
			case "channel":
				m.NotificationChannelIDs = []int64{missing}
			case "tag":
				m.MonitorTags = []MonitorTag{{TagID: missing}}
			case "agent":
				m.AgentIDs = []int64{missing}
			}
			err := s.CreateMonitorWithRelations(ctx, m, MonitorCreateOptions{})
			if !errors.Is(err, ErrInvalidMonitorRelation) {
				t.Fatalf("missing %s not classified: %v", kind, err)
			}
			if m.ID != 0 || !m.CreatedAt.IsZero() || m.Settings != nil {
				t.Fatal("failed creation published mutable output")
			}
			var count int
			if err := s.readDB.QueryRow("SELECT COUNT(*) FROM monitors").Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial monitor persisted: count=%d error=%v", count, err)
			}
		})
	}
	t.Run("non-FK failure remains distinct", func(t *testing.T) {
		s := testStore(t)
		if _, err := s.writeDB.Exec("CREATE TRIGGER reject_fixture BEFORE INSERT ON monitors BEGIN SELECT RAISE(ABORT,'fixture failure'); END"); err != nil {
			t.Fatal(err)
		}
		err := s.CreateMonitor(ctx, relationErrorMonitor())
		if err == nil || errors.Is(err, ErrInvalidMonitorRelation) {
			t.Fatalf("non-FK error misclassified: %v", err)
		}
	})
}

func TestMissingMonitorGroupRejectsEveryWritePath(t *testing.T) {
	s, _, monitors := accessFixture(t)
	ctx := context.Background()
	missing := int64(999999)
	created := relationErrorMonitor()
	created.GroupID = &missing
	if err := s.CreateMonitorWithRelations(ctx, created, MonitorCreateOptions{}, MonitorAccess{GroupIDs: []int64{missing}}); !errors.Is(err, ErrInvalidMonitorRelation) || created.ID != 0 {
		t.Fatalf("configured but nonexistent group admitted: %+v %v", created, err)
	}
	mon := monitors[0]
	original, err := s.GetMonitor(ctx, mon.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"legacy-update", "relations-update", "bulk"} {
		t.Run(kind, func(t *testing.T) {
			var err error
			switch kind {
			case "legacy-update":
				candidate := copyMonitorSnapshot(original)
				candidate.Name = "must roll back"
				candidate.GroupID = &missing
				stamp := candidate.UpdatedAt
				err = s.UpdateMonitor(ctx, candidate)
				if candidate.UpdatedAt != stamp {
					t.Fatal("failed update changed caller timestamp")
				}
			case "relations-update":
				var saved *Monitor
				saved, err = s.UpdateMonitorWithRelations(ctx, mon.ID, func(m *Monitor) (*Monitor, MonitorRelationChanges, error) {
					m.Name = "must roll back"
					m.GroupID = &missing
					return m, MonitorRelationChanges{Group: true}, nil
				})
				if saved != nil {
					t.Fatal("failed update returned a monitor")
				}
			case "bulk":
				_, err = s.BulkSetMonitorGroup(ctx, []int64{monitors[0].ID, monitors[1].ID}, &missing)
			}
			if !errors.Is(err, ErrInvalidMonitorRelation) {
				t.Fatalf("missing group not rejected: %v", err)
			}
			got, err := s.GetMonitor(ctx, mon.ID)
			if err != nil || got.Name != original.Name || got.GroupID == nil || original.GroupID == nil || *got.GroupID != *original.GroupID {
				t.Fatalf("invalid group mutation changed saved config: %+v %v", got, err)
			}
		})
	}
	denied := copyMonitorSnapshot(original)
	denied.GroupID = &missing
	if err := s.UpdateMonitor(ctx, denied, MonitorAccess{MonitorIDs: []int64{mon.ID}}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("denied destination disclosed group validity: %v", err)
	}
	badProxy := copyMonitorSnapshot(original)
	badProxy.ProxyID = &missing
	if err := s.UpdateMonitor(ctx, badProxy); !errors.Is(err, ErrInvalidMonitorRelation) {
		t.Fatalf("update FK not classified: %v", err)
	}
}

func TestGroupDeletionCannotRaceMonitorAdmission(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for iteration := range 20 {
		group := &MonitorGroup{Name: fmt.Sprintf("group-%d", iteration)}
		if err := s.CreateMonitorGroup(ctx, group); err != nil {
			t.Fatal(err)
		}
		m := relationErrorMonitor()
		m.GroupID = &group.ID
		start := make(chan struct{})
		var wg sync.WaitGroup
		var createErr, deleteErr error
		wg.Go(func() { <-start; createErr = s.CreateMonitor(ctx, m) })
		wg.Go(func() { <-start; deleteErr = s.DeleteMonitorGroup(ctx, group.ID) })
		close(start)
		wg.Wait()
		if deleteErr != nil || createErr != nil && !errors.Is(createErr, ErrInvalidMonitorRelation) {
			t.Fatalf("unexpected race result: create=%v delete=%v", createErr, deleteErr)
		}
		var orphanCount int
		if err := s.readDB.QueryRow("SELECT COUNT(*) FROM monitors m LEFT JOIN monitor_groups g ON g.id=m.group_id WHERE m.group_id IS NOT NULL AND g.id IS NULL").Scan(&orphanCount); err != nil || orphanCount != 0 {
			t.Fatalf("deleted group left orphan membership: %d %v", orphanCount, err)
		}
	}
}

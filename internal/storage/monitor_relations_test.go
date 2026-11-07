package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func TestMonitorRelationSaveRollsBackEveryField(t *testing.T) {
	store, _, monitors := accessFixture(t)
	ctx := context.Background()
	channel := &NotificationChannel{Name: "saved", Type: "webhook", Settings: []byte("{}"), Enabled: true}
	if err := store.CreateNotificationChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	monitor := monitors[0]
	if err := store.SetMonitorNotificationChannels(ctx, monitor.ID, []int64{channel.ID}); err != nil {
		t.Fatal(err)
	}
	originalName := monitor.Name
	access := MonitorAccess{MonitorIDs: []int64{monitor.ID}}
	for _, failure := range []string{"preparation", "relationship"} {
		t.Run(failure, func(t *testing.T) {
			result, err := store.UpdateMonitorWithRelations(ctx, monitor.ID, func(current *Monitor) (*Monitor, MonitorRelationChanges, error) {
				current.Name = "must roll back"
				current.NotificationChannelIDs = []int64{999999}
				if failure == "preparation" {
					return current, MonitorRelationChanges{}, errors.New("injected validation failure")
				}
				return current, MonitorRelationChanges{Notifications: true}, nil
			}, access)
			if err == nil || result != nil {
				t.Fatalf("failed mutation returned a saved monitor: %+v, %v", result, err)
			}
			stored, err := store.GetMonitor(ctx, monitor.ID)
			ids, idsErr := store.GetMonitorNotificationChannelIDs(ctx, monitor.ID)
			if err != nil || idsErr != nil || stored.Name != originalName || !reflect.DeepEqual(ids, []int64{channel.ID}) {
				t.Fatalf("failed save changed fields/relations: %+v %v, %v %v", stored, ids, err, idsErr)
			}
		})
	}
	created := &Monitor{Name: "must not exist", Type: "http", Target: "https://example.test", Interval: 60, Timeout: 10, NotificationChannelIDs: []int64{999999}}
	if err := store.CreateMonitorWithRelations(ctx, created, MonitorCreateOptions{}, MonitorAccess{All: true}); err == nil || created.ID != 0 || created.Settings != nil {
		t.Fatalf("failed create mutated caller or committed partial data: %+v, %v", created, err)
	}
	list, err := store.ListMonitors(ctx, MonitorListFilter{}, Pagination{Page: 1, PerPage: 100})
	if err != nil || list.Total != int64(len(monitors)) {
		t.Fatalf("failed create left monitor/status rows: %+v, %v", list, err)
	}
}

func TestMonitorUpdatePreservesCurrentHiddenRelationships(t *testing.T) {
	store, groups, monitors := accessFixture(t)
	ctx := context.Background()
	monitor := monitors[0]
	channel := &NotificationChannel{Name: "new assignment", Type: "webhook", Settings: []byte("{}"), Enabled: true}
	if err := store.CreateNotificationChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	// The form snapshot predates another administrator's new assignment/move.
	stale := copyMonitorSnapshot(monitor)
	if err := store.SetMonitorNotificationChannels(ctx, monitor.ID, []int64{channel.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BulkSetMonitorGroup(ctx, []int64{monitor.ID}, &groups[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMonitorEnabled(ctx, monitor.ID, false); err != nil {
		t.Fatal(err)
	}
	stale.ID = 999999
	stale.Name = "authorized configuration edit"
	stale.AgentEnabled = true
	stale.NotificationChannelIDs = nil
	stale.ProxyID = &stale.ID
	result, err := store.UpdateMonitorWithRelations(ctx, monitor.ID, func(current *Monitor) (*Monitor, MonitorRelationChanges, error) {
		if current.GroupID == nil || *current.GroupID != groups[1].ID || !reflect.DeepEqual(current.NotificationChannelIDs, []int64{channel.ID}) {
			t.Fatalf("preparation received stale relationships: %+v", current)
		}
		return stale, MonitorRelationChanges{}, nil
	}, MonitorAccess{MonitorIDs: []int64{monitor.ID}})
	if err != nil || result.ID != monitor.ID || result.GroupID == nil || *result.GroupID != groups[1].ID || result.ProxyID != nil || result.Enabled || result.AgentEnabled {
		t.Fatalf("hidden scalar fields restored stale assignments: %+v, %v", result, err)
	}
	if stale.ID != 999999 {
		t.Fatal("save mutated caller-owned candidate identity")
	}
	ids, err := store.GetMonitorNotificationChannelIDs(ctx, monitor.ID)
	if err != nil || !reflect.DeepEqual(ids, []int64{channel.ID}) {
		t.Fatalf("hidden relation was cleared by stale form: %v, %v", ids, err)
	}
}

func TestMonitorAtomicSaveChecksSourceAndDestination(t *testing.T) {
	store, groups, monitors := accessFixture(t)
	ctx := context.Background()
	called := false
	prepare := func(current *Monitor) (*Monitor, MonitorRelationChanges, error) {
		called = true
		current.GroupID = &groups[1].ID
		return current, MonitorRelationChanges{Group: true}, nil
	}
	if result, err := store.UpdateMonitorWithRelations(ctx, monitors[1].ID, prepare, MonitorAccess{GroupIDs: []int64{groups[0].ID}}); result != nil || !errors.Is(err, sql.ErrNoRows) || called {
		t.Fatalf("inaccessible source reached preparation: %+v, %v, called=%t", result, err, called)
	}
	if result, err := store.UpdateMonitorWithRelations(ctx, monitors[0].ID, prepare, MonitorAccess{MonitorIDs: []int64{monitors[0].ID}}); result != nil || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("ID-only grant moved to unassigned destination: %+v, %v", result, err)
	}
	if result, err := store.UpdateMonitorWithRelations(ctx, monitors[0].ID, prepare, MonitorAccess{MonitorIDs: []int64{monitors[0].ID}, GroupIDs: []int64{groups[1].ID}}); err != nil || result.GroupID == nil || *result.GroupID != groups[1].ID {
		t.Fatalf("authorized source/destination rejected: %+v, %v", result, err)
	}
}

type failingMonitorEntropy struct{}

func (failingMonitorEntropy) Read([]byte) (int, error) {
	return 0, errors.New("injected entropy failure")
}

func TestMonitorAtomicSaveRollsBackEncryptionFailure(t *testing.T) {
	store, _, monitors := accessFixture(t)
	ctx := context.Background()
	gcm, err := newGCM(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store.SetEncryptor(&Encryptor{gcm: gcm})
	originalReader := rand.Reader
	rand.Reader = failingMonitorEntropy{}
	defer func() { rand.Reader = originalReader }()
	result, err := store.UpdateMonitorWithRelations(ctx, monitors[0].ID, func(current *Monitor) (*Monitor, MonitorRelationChanges, error) {
		current.Name = "must roll back"
		current.Settings = []byte(`{"password":"new secret"}`)
		return current, MonitorRelationChanges{}, nil
	}, MonitorAccess{All: true})
	rand.Reader = originalReader
	if err == nil || result != nil {
		t.Fatalf("encryption failure returned success: %+v, %v", result, err)
	}
	stored, err := store.GetMonitor(ctx, monitors[0].ID)
	if err != nil || stored.Name != monitors[0].Name || string(stored.Settings) != "{}" {
		t.Fatalf("encryption failure changed persisted monitor: %+v, %v", stored, err)
	}
}

func TestMonitorAtomicSaveReturnsCurrentStatus(t *testing.T) {
	store, _, monitors := accessFixture(t)
	ctx := context.Background()
	id := monitors[0].ID
	for _, enabled := range []bool{false, true} {
		saved, err := store.UpdateMonitorWithRelations(ctx, id, func(current *Monitor) (*Monitor, MonitorRelationChanges, error) {
			current.Enabled = enabled
			current.Status = "caller supplied"
			current.ConsecFails = 999
			return current, MonitorRelationChanges{Enabled: true}, nil
		}, MonitorAccess{All: true})
		if err != nil {
			t.Fatal(err)
		}
		want := "paused"
		if enabled {
			want = "pending"
		}
		if saved.Status != want || saved.ConsecFails != 0 || saved.Enabled != enabled {
			t.Fatalf("saved response contains stale or caller-supplied runtime fields: %+v", saved)
		}
	}
}

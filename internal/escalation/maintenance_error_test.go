package escalation

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/y0f/asura/v2/internal/notifier"
	"github.com/y0f/asura/v2/internal/storage"
)

type maintenanceErrorStore struct {
	storage.Store
	err error
}

func (s *maintenanceErrorStore) IsMonitorInMaintenance(ctx context.Context, id int64, now time.Time) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return s.Store.IsMonitorInMaintenance(ctx, id, now)
}

type maintenanceRecordingSender struct{ sent atomic.Int64 }

func (*maintenanceRecordingSender) Type() string { return "webhook" }

func (s *maintenanceRecordingSender) Send(context.Context, *storage.NotificationChannel, *notifier.Payload) error {
	s.sent.Add(1)
	return nil
}

func TestEscalationMaintenanceReadFailureRetainsStepAndRecovers(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		name := "single"
		if repeat {
			name = "repeat"
		}
		t.Run(name, func(t *testing.T) {
			store := testStore(t)
			ctx := context.Background()
			_, inc, _ := escalationFixture(t, store, 0, repeat)
			before, err := store.GetEscalationStateByIncident(ctx, inc.ID)
			if err != nil {
				t.Fatal(err)
			}
			fault := &maintenanceErrorStore{Store: store, err: errors.New("maintenance lookup unavailable")}
			dispatcher := notifier.NewDispatcher(store, testLogger(), false)
			sender := &maintenanceRecordingSender{}
			dispatcher.RegisterSender(sender) // No external delivery, even in the failure baseline.
			t.Cleanup(func() { dispatcher.Shutdown(context.Background()) })
			runner := NewRunner(fault, dispatcher, testLogger())

			for attempt := 0; attempt < 2; attempt++ {
				runner.Tick(ctx)
				if stats := dispatcher.Stats(); stats.Accepted != 0 || stats.Rejected != 0 {
					t.Errorf("unreadable maintenance attempted notification admission: %+v", stats)
				}
				after, err := store.GetEscalationStateByIncident(ctx, inc.ID)
				if err != nil {
					t.Fatalf("unreadable maintenance consumed durable step: %v", err)
				}
				if after.CurrentStep != before.CurrentStep || !after.NextFireAt.Equal(before.NextFireAt) {
					t.Fatalf("unreadable maintenance advanced durable step: before=%+v after=%+v", before, after)
				}
			}

			fault.err = nil
			runner.Tick(ctx)
			runner.Tick(ctx) // Recovery must not deliver the already-consumed step twice.
			drainCtx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			if err := dispatcher.Shutdown(drainCtx); err != nil {
				t.Fatal(err)
			}
			if stats := dispatcher.Stats(); stats.Accepted != 1 || stats.Delivered != 1 || sender.sent.Load() != 1 {
				t.Fatalf("recovered step was not delivered exactly once in this run: stats=%+v sent=%d", stats, sender.sent.Load())
			}
			after, err := store.GetEscalationStateByIncident(ctx, inc.ID)
			if repeat {
				if err != nil || after.CurrentStep != 0 || !after.NextFireAt.After(before.NextFireAt) {
					t.Fatalf("recovered repeat was not rescheduled: %+v %v", after, err)
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("recovered single step remains: %+v %v", after, err)
			}
		})
	}
}

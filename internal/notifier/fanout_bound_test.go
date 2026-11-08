package notifier

import (
	"context"
	"io"
	"log/slog"
	"runtime"
	"testing"
	"time"

	"github.com/y0f/asura/v2/internal/storage"
)

type heldSender struct{ release <-chan struct{} }

func (heldSender) Type() string { return "held" }
func (s heldSender) Send(ctx context.Context, _ *storage.NotificationChannel, _ *Payload) error {
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Keep this behavioral regression runnable against the old public API too:
// a burst of recipients must not create one waiting goroutine per recipient.
func TestNotificationBurstHasBoundedGoroutines(t *testing.T) {
	st := &historyStore{}
	for id := range 512 {
		st.channels = append(st.channels, &storage.NotificationChannel{ID: int64(id + 1), Type: "held", Enabled: true})
	}
	d := NewDispatcher(st, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	d.attemptTimeout = time.Minute
	release := make(chan struct{})
	d.RegisterSender(heldSender{release})
	defer close(release)
	if owner, ok := any(d).(interface{ Shutdown(context.Context) error }); ok {
		defer func() { ctx, cancel := context.WithCancel(context.Background()); cancel(); owner.Shutdown(ctx) }()
	}
	base := runtime.NumGoroutine()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	p := &Payload{EventType: "test"}
	switch dispatcher := any(d).(type) {
	case interface {
		NotifyWithPayload(context.Context, *Payload) error
	}:
		_ = dispatcher.NotifyWithPayload(ctx, p)
	case interface{ NotifyWithPayload(*Payload) }:
		dispatcher.NotifyWithPayload(p)
	default:
		t.Fatal("unknown notification API")
	}
	if added := runtime.NumGoroutine() - base; added > 20 {
		t.Fatalf("512 recipients created %d goroutines; want a fixed worker pool", added)
	}
}

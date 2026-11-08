package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/v2/internal/monitor"
	"github.com/y0f/asura/v2/internal/notifier"
	"github.com/y0f/asura/v2/internal/storage"
)

type forwardStore struct{ storage.Store }

func (*forwardStore) ListNotificationChannels(ctx context.Context) ([]*storage.NotificationChannel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []*storage.NotificationChannel{{ID: 1, Type: "fixture", Enabled: true}}, nil
}
func (*forwardStore) InsertNotificationHistory(context.Context, *storage.NotificationHistory) error {
	return nil
}

type forwardSender struct{}

func (forwardSender) Type() string { return "fixture" }
func (forwardSender) Send(context.Context, *storage.NotificationChannel, *notifier.Payload) error {
	return nil
}

func TestNotificationForwarderDrainsAfterProducersJoin(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "drain", true: "account-cancelled"}[canceled], func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			d := notifier.NewDispatcher(&forwardStore{}, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
			d.RegisterSender(forwardSender{})
			defer d.Shutdown(context.Background())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if canceled {
				cancel()
			}
			joined, done := make(chan struct{}), make(chan struct{})
			events := make(chan monitor.NotificationEvent, 2)
			go func() { defer close(done); forwardNotifications(ctx, joined, events, d, nil, nil, logger) }()
			select {
			case <-done:
				t.Fatal("forwarder stopped before producers joined")
			case <-time.After(10 * time.Millisecond):
			}
			for range 2 {
				events <- monitor.NotificationEvent{EventType: "incident.created"}
			}
			close(joined)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("forwarder did not drain")
			}
			d.Shutdown(context.Background())
			if len(events) != 0 {
				t.Fatal("accepted pipeline events were discarded")
			}
			if canceled {
				if strings.Count(logs.String(), "forward notification") != 2 {
					t.Fatalf("missing explicit rejected-event accounting: %s", logs.String())
				}
			} else if d.Stats().Delivered != 2 {
				t.Fatalf("queued events not delivered: %+v", d.Stats())
			}
		})
	}
}

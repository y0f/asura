package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNotificationUpdateUsesCurrentRowAndRollsBack(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := &NotificationChannel{Name: "original", Type: "webhook", Settings: json.RawMessage(`{"url":"https://old.test"}`)}
	if err := s.CreateNotificationChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		_, err := s.UpdateNotificationChannelWith(ctx, ch.ID, func(current *NotificationChannel) (*NotificationChannel, error) {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			current.Settings = json.RawMessage(`{"url":"https://new.test"}`)
			current.Events = []string{"incident.created"}
			return current, nil
		})
		first <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first update never entered")
	}
	second := make(chan error, 1)
	go func() {
		_, err := s.UpdateNotificationChannelWith(ctx, ch.ID, func(current *NotificationChannel) (*NotificationChannel, error) {
			if !strings.Contains(string(current.Settings), "new.test") || len(current.Events) != 1 {
				return nil, errors.New("stale credentials or events")
			}
			current.Name = "renamed"
			return current, nil
		})
		second <- err
	}()
	close(release)
	for _, result := range []<-chan error{first, second} {
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	}
	rejected := errors.New("reject")
	if _, err := s.UpdateNotificationChannelWith(ctx, ch.ID, func(current *NotificationChannel) (*NotificationChannel, error) {
		current.Name, current.Settings, current.Events = "rejected", nil, nil
		return current, rejected
	}); !errors.Is(err, rejected) {
		t.Fatal("callback error was ignored")
	}
	got, err := s.GetNotificationChannel(ctx, ch.ID)
	if err != nil || got.Name != "renamed" || !strings.Contains(string(got.Settings), "new.test") || len(got.Events) != 1 || !got.CreatedAt.Equal(ch.CreatedAt) {
		t.Fatal("rollback or concurrent field preservation failed")
	}
}

func TestNotificationUnreadableCredentialAndEventsFailClosed(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ch := &NotificationChannel{Name: "protected", Type: "webhook", Settings: json.RawMessage(`{"url":"https://secret.test"}`)}
	if err := s.CreateNotificationChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE notification_channels SET settings='enc:unreadable-secret'`,
		`UPDATE notification_channels SET settings='{}',events='{invalid'`,
	} {
		if _, err := s.writeDB.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetNotificationChannel(ctx, ch.ID); err == nil || strings.Contains(err.Error(), "unreadable-secret") {
			t.Fatal("invalid stored channel did not fail closed")
		}
		if got, err := s.ListNotificationChannels(ctx); err == nil || got != nil {
			t.Fatal("invalid stored channel returned a partial list")
		}
		called := false
		if _, err := s.UpdateNotificationChannelWith(ctx, ch.ID, func(current *NotificationChannel) (*NotificationChannel, error) {
			called = true
			return current, nil
		}); err == nil || called {
			t.Fatal("invalid stored state was passed to update")
		}
	}
}

package storage

import (
	"context"
	"testing"
)

func TestSubscriberCRUD(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	sp := &StatusPage{
		Slug:    "test",
		Title:   "Test Page",
		Enabled: true,
	}
	if err := store.CreateStatusPage(ctx, sp); err != nil {
		t.Fatal(err)
	}

	t.Run("create email subscriber", func(t *testing.T) {
		sub := &StatusPageSubscriber{
			StatusPageID: sp.ID,
			Type:         "email",
			Email:        "test@example.com",
		}
		if err := store.CreateStatusPageSubscriber(ctx, sub); err != nil {
			t.Fatal(err)
		}
		if sub.ID == 0 {
			t.Fatal("expected non-zero ID")
		}
		if sub.Token == "" {
			t.Fatal("expected non-empty token")
		}
		if sub.Confirmed {
			t.Fatal("expected unconfirmed")
		}
	})

	t.Run("create webhook subscriber", func(t *testing.T) {
		sub := &StatusPageSubscriber{
			StatusPageID: sp.ID,
			Type:         "webhook",
			WebhookURL:   "https://example.com/hook",
			Confirmed:    true,
		}
		if err := store.CreateStatusPageSubscriber(ctx, sub); err != nil {
			t.Fatal(err)
		}
		if sub.ID == 0 {
			t.Fatal("expected non-zero ID")
		}
		if !sub.Confirmed {
			t.Fatal("expected confirmed")
		}
	})

	t.Run("count only confirmed", func(t *testing.T) {
		count, err := store.CountSubscribersByPage(ctx, sp.ID)
		if err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("expected 1 confirmed, got %d", count)
		}
	})

	t.Run("confirm email subscriber", func(t *testing.T) {
		sub := &StatusPageSubscriber{
			StatusPageID: sp.ID,
			Type:         "email",
			Email:        "confirm@example.com",
		}
		if err := store.CreateStatusPageSubscriber(ctx, sub); err != nil {
			t.Fatal(err)
		}

		if err := store.ConfirmSubscriber(ctx, sub.Token); err != nil {
			t.Fatal(err)
		}

		got, err := store.GetSubscriberByToken(ctx, sub.Token)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Confirmed {
			t.Fatal("expected confirmed after confirm call")
		}
		if got.Email != "confirm@example.com" {
			t.Fatalf("expected confirm@example.com, got %s", got.Email)
		}
	})

	t.Run("confirm already confirmed returns error", func(t *testing.T) {
		sub := &StatusPageSubscriber{
			StatusPageID: sp.ID,
			Type:         "email",
			Email:        "already@example.com",
		}
		if err := store.CreateStatusPageSubscriber(ctx, sub); err != nil {
			t.Fatal(err)
		}
		if err := store.ConfirmSubscriber(ctx, sub.Token); err != nil {
			t.Fatal(err)
		}
		if err := store.ConfirmSubscriber(ctx, sub.Token); err == nil {
			t.Fatal("expected error confirming already confirmed")
		}
	})

	t.Run("list confirmed subscribers", func(t *testing.T) {
		subs, err := store.ListConfirmedSubscribers(ctx, sp.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(subs) != 3 {
			t.Fatalf("expected 3 confirmed, got %d", len(subs))
		}
	})

	t.Run("delete by token", func(t *testing.T) {
		sub := &StatusPageSubscriber{
			StatusPageID: sp.ID,
			Type:         "webhook",
			WebhookURL:   "https://delete.example.com",
			Confirmed:    true,
		}
		if err := store.CreateStatusPageSubscriber(ctx, sub); err != nil {
			t.Fatal(err)
		}

		if err := store.DeleteSubscriberByToken(ctx, sub.Token); err != nil {
			t.Fatal(err)
		}

		_, err := store.GetSubscriberByToken(ctx, sub.Token)
		if err == nil {
			t.Fatal("expected error after deletion")
		}
	})

	t.Run("delete by id", func(t *testing.T) {
		sub := &StatusPageSubscriber{
			StatusPageID: sp.ID,
			Type:         "webhook",
			WebhookURL:   "https://delete-id.example.com",
			Confirmed:    true,
		}
		if err := store.CreateStatusPageSubscriber(ctx, sub); err != nil {
			t.Fatal(err)
		}

		if err := store.DeleteSubscriber(ctx, sub.ID); err != nil {
			t.Fatal(err)
		}

		if err := store.DeleteSubscriber(ctx, sub.ID); err == nil {
			t.Fatal("expected error deleting non-existent subscriber")
		}
	})

	t.Run("invalid token returns error", func(t *testing.T) {
		_, err := store.GetSubscriberByToken(ctx, "nonexistent-token")
		if err == nil {
			t.Fatal("expected error for invalid token")
		}
	})

	t.Run("delete nonexistent token returns error", func(t *testing.T) {
		err := store.DeleteSubscriberByToken(ctx, "nonexistent-token-value")
		if err == nil {
			t.Fatal("expected error deleting nonexistent token")
		}
	})
}

func TestGetStatusPageIDsForMonitor(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	m := &Monitor{
		Name:             "API",
		Type:             "http",
		Target:           "https://example.com",
		Interval:         60,
		Timeout:          10,
		Enabled:          true,
		FailureThreshold: 3,
		SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}

	sp1 := &StatusPage{Slug: "page1", Title: "Page 1", Enabled: true}
	sp2 := &StatusPage{Slug: "page2", Title: "Page 2", Enabled: true}
	sp3 := &StatusPage{Slug: "page3", Title: "Page 3", Enabled: false}
	if err := store.CreateStatusPage(ctx, sp1); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateStatusPage(ctx, sp2); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateStatusPage(ctx, sp3); err != nil {
		t.Fatal(err)
	}

	if err := store.SetStatusPageMonitors(ctx, sp1.ID, []StatusPageMonitor{{PageID: sp1.ID, MonitorID: m.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatusPageMonitors(ctx, sp2.ID, []StatusPageMonitor{{PageID: sp2.ID, MonitorID: m.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatusPageMonitors(ctx, sp3.ID, []StatusPageMonitor{{PageID: sp3.ID, MonitorID: m.ID}}); err != nil {
		t.Fatal(err)
	}

	ids, err := store.GetStatusPageIDsForMonitor(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 enabled pages, got %d", len(ids))
	}

	ids2, err := store.GetStatusPageIDsForMonitor(ctx, 9999)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids2) != 0 {
		t.Fatalf("expected 0 pages for non-existent monitor, got %d", len(ids2))
	}
}

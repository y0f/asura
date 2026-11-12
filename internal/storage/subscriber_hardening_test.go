package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPublicSubscriberDedupeAndUnconfirmedExpiry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	page := &StatusPage{Title: "Status", Slug: "status-hardening", Enabled: true, APIEnabled: true}
	if err := s.CreateStatusPage(ctx, page); err != nil {
		t.Fatal(err)
	}
	access := PublicStatusPageAccess{Audience: PublicStatusPageAPISubscription}
	first := &StatusPageSubscriber{StatusPageID: page.ID, Type: "email", Email: "Person@Example.com"}
	if _, err := s.CreatePublicStatusPageSubscriber(ctx, first, access); err != nil {
		t.Fatal(err)
	}
	again := &StatusPageSubscriber{StatusPageID: page.ID, Type: "email", Email: "person@example.com"}
	if _, err := s.CreatePublicStatusPageSubscriber(ctx, again, access); !errors.Is(err, ErrSubscriberExists) {
		t.Fatalf("same address in another case was accepted again: %v", err)
	}
	confirmed := &StatusPageSubscriber{StatusPageID: page.ID, Type: "webhook", WebhookURL: "https://example.com/h", Confirmed: true}
	if _, err := s.CreatePublicStatusPageSubscriber(ctx, confirmed, access); err != nil {
		t.Fatal(err)
	}
	// Age the unconfirmed email past its confirmation window.
	old := formatTime(time.Now().Add(-UnconfirmedSubscriberLifetime - time.Hour))
	if _, err := s.writeDB.ExecContext(ctx, "UPDATE status_page_subscribers SET created_at=? WHERE id=?", old, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PurgeOldData(ctx, time.Now().Add(-90*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSubscriberByToken(ctx, first.Token); err == nil {
		t.Fatal("an expired unconfirmed subscription survived retention")
	}
	if _, err := s.GetSubscriberByToken(ctx, confirmed.Token); err != nil {
		t.Fatalf("retention removed a confirmed subscriber: %v", err)
	}
	if _, err := s.CreatePublicStatusPageSubscriber(ctx, again, access); err != nil {
		t.Fatalf("the address could not subscribe again after expiry: %v", err)
	}
}

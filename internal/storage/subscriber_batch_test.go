package storage

import (
	"context"
	"fmt"
	"testing"
)

func TestConfirmedSubscriberKeysetBatches(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	page := &StatusPage{Slug: "batch", Title: "batch", Enabled: true}
	other := &StatusPage{Slug: "other", Title: "other", Enabled: true}
	for _, p := range []*StatusPage{page, other} {
		if err := s.CreateStatusPage(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	var want []int64
	for i := range 17 {
		sub := &StatusPageSubscriber{StatusPageID: page.ID, Type: "email", Email: fmt.Sprintf("%d@example.test", i), Confirmed: i%3 != 0}
		if i%4 == 0 {
			sub.StatusPageID = other.ID
		}
		if err := s.CreateStatusPageSubscriber(ctx, sub); err != nil {
			t.Fatal(err)
		}
		if sub.Confirmed && sub.StatusPageID == page.ID {
			want = append(want, sub.ID)
		}
	}
	var after int64
	var got []int64
	for {
		batch, err := s.ListConfirmedSubscribersAfter(ctx, page.ID, after, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(batch) > 3 {
			t.Fatal("unbounded batch")
		}
		for _, sub := range batch {
			if sub.ID <= after || !sub.Confirmed || sub.StatusPageID != page.ID {
				t.Fatalf("invalid batch row: %+v", sub)
			}
			after = sub.ID
			got = append(got, sub.ID)
		}
		if len(batch) < 3 {
			break
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("keyset skipped/duplicated recipients: %v want %v", got, want)
	}
}

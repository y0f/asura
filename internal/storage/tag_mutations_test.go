package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestTagMutationsReportCurrentRowsAndConflicts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := &Tag{Name: "first", Color: "#005bbf"}
	b := &Tag{Name: "second", Color: "#005bbf"}
	for _, tag := range []*Tag{a, b} {
		if err := s.CreateTag(ctx, tag); err != nil {
			t.Fatal(err)
		}
	}
	edit := *a
	edit.Name, edit.CreatedAt = b.Name, time.Unix(1, 0)
	if err := s.UpdateTag(ctx, &edit); !errors.Is(err, ErrTagNameConflict) || !edit.CreatedAt.Equal(time.Unix(1, 0)) {
		t.Fatalf("conflicting edit changed output or lost classification: %v", err)
	}
	stored, err := s.GetTag(ctx, a.ID)
	if err != nil || stored.Name != a.Name || !stored.CreatedAt.Equal(a.CreatedAt) {
		t.Fatalf("conflicting edit changed stored row: %+v, %v", stored, err)
	}
	edit.Name = "updated"
	if err := s.UpdateTag(ctx, &edit); err != nil || !edit.CreatedAt.Equal(a.CreatedAt) {
		t.Fatalf("successful edit did not return original creation time: %v", err)
	}
	if err := s.DeleteTag(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateTag(ctx, &edit); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing update reported success: %v", err)
	}
	if err := s.DeleteTag(ctx, a.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing delete reported success: %v", err)
	}
}

func TestTagConcurrentCreateReturnsNameConflict(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			tag := &Tag{Name: "same name", Color: "#005bbf"}
			err := s.CreateTag(ctx, tag)
			if err != nil && (tag.ID != 0 || !tag.CreatedAt.IsZero()) {
				results <- errors.New("failed create mutated output")
				return
			}
			results <- err
		}()
	}
	close(start)
	success, conflict := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrTagNameConflict):
			conflict++
		default:
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrent create: successes=%d conflicts=%d", success, conflict)
	}
}

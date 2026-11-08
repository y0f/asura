package storage

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSQLGuardBlocksExecutionAndCommit(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	var lost atomic.Bool
	s.writeDB.guard = func() error {
		if lost.Load() {
			return ErrDatabaseOwnershipLost
		}
		return nil
	}
	defer func() { lost.Store(false) }()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO tags(name) VALUES ('must-rollback')"); err != nil {
		t.Fatal(err)
	}
	lost.Store(true)
	if err := tx.Commit(); !errors.Is(err, ErrDatabaseOwnershipLost) {
		t.Fatalf("guarded commit succeeded: %v", err)
	}
	if _, err := s.writeDB.ExecContext(ctx, "INSERT INTO tags(name) VALUES ('must-not-execute')"); !errors.Is(err, ErrDatabaseOwnershipLost) {
		t.Fatalf("guarded write succeeded: %v", err)
	}
	var id int64
	if err := s.writeDB.QueryRowContext(ctx, "INSERT INTO tags(name) VALUES ('must-not-return') RETURNING id").Scan(&id); !errors.Is(err, context.Canceled) {
		t.Fatalf("guarded returning write succeeded: %v", err)
	}
	var count int
	if err := s.readDB.QueryRowContext(ctx, "SELECT count(*) FROM tags").Scan(&count); err != nil || count != 0 {
		t.Fatalf("ownership loss left writes: %d %v", count, err)
	}
}

func TestSQLStoreCloseJoinsConcurrentCallers(t *testing.T) {
	s := testStore(t)
	var stopped atomic.Int32
	s.beforeClose = func() error { stopped.Add(1); return nil }
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if stopped.Load() != 1 {
		t.Fatalf("shutdown ran %d times", stopped.Load())
	}
}

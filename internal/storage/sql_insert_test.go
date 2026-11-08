package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestGeneratedKeysIdentifyCommittedRows(t *testing.T) {
	testGeneratedKeys(t, testStore(t))
}

// The same contract is used by the optional PostgreSQL integration suite.
func testGeneratedKeys(t *testing.T, store *SQLStore) {
	t.Helper()
	ctx := context.Background()
	var workers sync.WaitGroup
	for i := range 12 {
		workers.Go(func() {
			tag := &Tag{Name: fmt.Sprintf("generated-key-%d", i), Color: "#111111"}
			if err := store.CreateTag(ctx, tag); err != nil {
				t.Errorf("create tag: %v", err)
				return
			}
			got, err := store.GetTag(ctx, tag.ID)
			if err != nil || got.Name != tag.Name || tag.ID <= 0 {
				t.Errorf("generated key %d does not identify %q: got %+v, error %v", tag.ID, tag.Name, got, err)
			}
		})
	}
	workers.Wait()

	failed := &Tag{ID: 987654, Name: "generated-key-0", Color: "#222222"}
	if err := store.CreateTag(ctx, failed); err == nil || failed.ID != 987654 || !failed.CreatedAt.IsZero() {
		t.Fatalf("failed insert changed caller output: %+v, %v", failed, err)
	}

	tx, err := store.writeDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := insertID(ctx, tx, `INSERT INTO tags (name,color,created_at) VALUES (?,?,?)`, "rolled-back-key", "#333333", "2026-01-01T00:00:00Z")
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetTag(ctx, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rolled-back generated key remained visible: %v", err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := insertID(canceled, store.writeDB, `INSERT INTO tags (name,color,created_at) VALUES (?,?,?)`, "canceled-key", "#444444", "2026-01-01T00:00:00Z"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled insert error = %v", err)
	}
	var count int
	if err := store.readDB.QueryRowContext(ctx, "SELECT count(*) FROM tags WHERE name IN (?,?)", "rolled-back-key", "canceled-key").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed inserts left rows: count=%d, error=%v", count, err)
	}
}

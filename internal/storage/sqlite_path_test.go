package storage

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSQLiteFilenameReservedCharactersRemainLiteral(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	names := []string{"data#first.db", "data#second.db", "data%23encoded.db", "data with spaces.db"}
	if runtime.GOOS != "windows" {
		// Windows does not allow '?' in file names; Unix does.
		names = append(names, "data?mode=memory.db")
	}
	for _, name := range names {
		path := filepath.Join(root, name)
		store, err := NewSQLiteStore(path, 1)
		if err != nil {
			t.Fatalf("open literal filename %q: %v", name, err)
		}
		if err := store.CreateTOTPKey(ctx, &TOTPKey{APIKeyName: "same-key", Secret: name}); err != nil {
			store.Close()
			t.Fatalf("distinct filenames shared a database: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("SQLite did not use the exact filesystem name %q: %v", name, err)
		}
		store, err = NewSQLiteStore(path, 1)
		if err != nil {
			t.Fatal(err)
		}
		key, err := store.GetTOTPKey(ctx, "same-key")
		store.Close()
		if err != nil || key.Secret != name {
			t.Fatalf("reopening the literal filename read another database: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "data")); !os.IsNotExist(err) {
		t.Fatalf("fragment/query truncation created a different file: %v", err)
	}
}

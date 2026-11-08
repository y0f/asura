package storage

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"
)

func TestConcurrentEncryptionInitializationKeepsOneSalt(t *testing.T) {
	store := testStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Hold the sole writer so every caller is waiting on its initial read.
	// This queues competing initial reads; the pool need not serve them FIFO.
	held, err := store.writeDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	before := store.writeDB.Stats().WaitCount
	const callers = 24
	salts := make([][]byte, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() { salts[i], errs[i] = store.EnsureKDFSalt(ctx) })
	}
	for store.writeDB.Stats().WaitCount-before < callers {
		if ctx.Err() != nil {
			t.Fatal("encryption callers did not queue")
		}
		time.Sleep(time.Millisecond)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	persisted, err := store.EnsureKDFSalt(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, salt := range salts {
		if errs[i] != nil || len(salt) != 16 || !bytes.Equal(salt, persisted) {
			t.Fatalf("caller %d used a different encryption salt: %v", i, errs[i])
		}
	}
}

func TestMalformedEncryptionSaltIsNotOverwritten(t *testing.T) {
	for _, value := range []string{"", "not-hex", "0011"} {
		t.Run(value, func(t *testing.T) {
			store := testStore(t)
			ctx := context.Background()
			if _, err := store.writeDB.ExecContext(ctx, "INSERT INTO meta (key,value) VALUES ('kdf_salt',?)", value); err != nil {
				t.Fatal(err)
			}
			if _, err := store.EnsureKDFSalt(ctx); err == nil {
				t.Fatal("invalid persisted encryption salt accepted")
			}
			var got string
			if err := store.readDB.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='kdf_salt'").Scan(&got); err != nil || got != value {
				t.Fatalf("invalid salt was silently replaced: %v", err)
			}
		})
	}
}

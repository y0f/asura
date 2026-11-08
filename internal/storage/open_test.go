package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOpenSQLiteInitializesEncryptionBeforeReturning(t *testing.T) {
	ctx := context.Background()
	for _, driver := range []string{"", "sqlite"} {
		t.Run(driver, func(t *testing.T) {
			options := Options{Driver: driver, Path: filepath.Join(t.TempDir(), "asura.db"), MaxReadConns: 2, EncryptionKey: "synthetic-key"}
			store, err := Open(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			key := &TOTPKey{APIKeyName: "test", Secret: "synthetic-totp-secret"}
			if err := store.CreateTOTPKey(ctx, key); err != nil {
				store.Close()
				t.Fatal(err)
			}
			var encoded string
			if err := store.(*SQLiteStore).readDB.QueryRow("SELECT secret FROM totp_keys WHERE api_key_name='test'").Scan(&encoded); err != nil || !strings.HasPrefix(encoded, encPrefix) || strings.Contains(encoded, key.Secret) {
				store.Close()
				t.Fatalf("Open returned before encryption was installed: %v", err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = Open(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			got, err := store.GetTOTPKey(ctx, key.APIKeyName)
			if err != nil || got.Secret != key.Secret {
				t.Fatalf("reopening did not retain the key derivation salt: %v", err)
			}
		})
	}
}

func TestOpenRejectsInvalidOptionsBeforeCreatingDatabase(t *testing.T) {
	const credential = "synthetic-private-dsn"
	for _, options := range []Options{
		{Driver: credential, MaxReadConns: 1},
		{Driver: "sqlite", DSN: credential, MaxReadConns: 1},
		{Driver: "postgres", MaxReadConns: 1},
		{Driver: "sqlite", MaxReadConns: 0},
	} {
		options.Path = filepath.Join(t.TempDir(), "must-not-exist.db")
		store, err := Open(context.Background(), options)
		if err == nil || store != nil || strings.Contains(err.Error(), credential) {
			t.Fatalf("invalid options accepted or credential exposed: %v", err)
		}
		if _, err := os.Stat(options.Path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid options opened SQLite: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "cancelled.db")
	if store, err := Open(ctx, Options{Path: path, MaxReadConns: 1}); store != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled startup opened a database: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled startup created a database: %v", err)
	}
}

type startupTestStore struct {
	Store
	events      []string
	saltErr     error
	finalizeErr error
	closeErr    error
}

func (s *startupTestStore) EnsureKDFSalt(context.Context) ([]byte, error) {
	s.events = append(s.events, "salt")
	return []byte("synthetic-salt-16"), s.saltErr
}

func (s *startupTestStore) SetEncryptor(*Encryptor) {
	s.events = append(s.events, "encryptor")
}

func (s *startupTestStore) FinalizeLegacyAgentAssignments(context.Context) error {
	s.events = append(s.events, "finalize")
	return s.finalizeErr
}

func (s *startupTestStore) Close() error {
	s.events = append(s.events, "close")
	return s.closeErr
}

func TestOpenInitializationOrderingAndFailureCleanup(t *testing.T) {
	failure := errors.New("injected startup failure")
	closeFailure := errors.New("injected close failure")
	for _, tc := range []struct {
		name  string
		store startupTestStore
		want  []string
	}{
		{"success", startupTestStore{}, []string{"salt", "encryptor", "finalize"}},
		{"salt failure", startupTestStore{saltErr: failure}, []string{"salt", "close"}},
		{"migration failure", startupTestStore{finalizeErr: failure, closeErr: closeFailure}, []string{"salt", "encryptor", "finalize", "close"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := initializeStore(context.Background(), &tc.store, "synthetic-key")
			if tc.store.saltErr != nil || tc.store.finalizeErr != nil {
				if store != nil || !errors.Is(err, failure) || tc.store.closeErr != nil && !errors.Is(err, closeFailure) {
					t.Fatalf("failed startup returned a store or lost its errors: %v", err)
				}
			} else if err != nil || store != &tc.store {
				t.Fatalf("successful initialization lost its store: %v", err)
			}
			if !reflect.DeepEqual(tc.store.events, tc.want) {
				t.Fatalf("startup ordering: %v, want %v", tc.store.events, tc.want)
			}
		})
	}
}

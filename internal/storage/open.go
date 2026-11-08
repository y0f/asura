package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Options contains connection and at-rest encryption settings. DSN and
// EncryptionKey are credentials and must not be included in logs.
type Options struct {
	Driver        string
	Path          string
	DSN           string
	MaxReadConns  int
	EncryptionKey string
	// RuntimeOwner is set by the server, not by offline account-maintenance
	// commands. PostgreSQL then requires exclusive runtime ownership.
	RuntimeOwner bool
	// OnOwnershipLoss must be nonblocking and must not call Close; the server
	// uses its cancel function so shutdown joins the observer from another goroutine.
	OnOwnershipLoss func()
}

var ErrBackendUnavailable = errors.New("database backend is not included in this build")

var (
	ErrDatabaseOwned         = errors.New("another Asura server owns this database")
	ErrDatabaseOwnershipLost = errors.New("database ownership was lost; restart Asura")
)

type initializableStore interface {
	Store
	SetEncryptor(*Encryptor)
	FinalizeLegacyAgentAssignments(context.Context) error
}

// An optional build registers its opener during package initialization. Keeping
// this nil in the default build avoids linking a second database driver.
var postgresOpener func(context.Context, Options) (initializableStore, error)

// Open returns a fully initialized store. No caller may start readers or
// workers before encryption and the legacy assignment migration are ready.
// An omitted driver preserves the existing SQLite configuration contract.
func Open(ctx context.Context, options Options) (Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.MaxReadConns <= 0 {
		return nil, errors.New("database.max_read_conns must be positive")
	}
	var store initializableStore
	var err error
	switch options.Driver {
	case "", "sqlite":
		if options.Path == "" {
			return nil, errors.New("database.path is required for SQLite")
		}
		if options.DSN != "" {
			return nil, errors.New("database.dsn is only supported by PostgreSQL")
		}
		store, err = NewSQLiteStore(options.Path, options.MaxReadConns)
	case "postgres":
		if strings.TrimSpace(options.DSN) == "" {
			return nil, errors.New("database.dsn is required for PostgreSQL")
		}
		if postgresOpener == nil {
			return nil, fmt.Errorf("%w: PostgreSQL requires a build with the postgres tag", ErrBackendUnavailable)
		}
		store, err = postgresOpener(ctx, options)
	default:
		return nil, errors.New("database.driver must be sqlite or postgres")
	}
	if err != nil {
		return nil, err
	}
	return initializeStore(ctx, store, options.EncryptionKey)
}

func initializeStore(ctx context.Context, store initializableStore, key string) (_ Store, err error) {
	defer func() {
		if err != nil {
			err = errors.Join(err, store.Close())
		}
	}()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if key != "" {
		salt, saltErr := store.EnsureKDFSalt(ctx)
		if saltErr != nil {
			return nil, fmt.Errorf("load encryption salt: %w", saltErr)
		}
		enc, encryptionErr := NewEncryptor(key, salt)
		if encryptionErr != nil {
			return nil, fmt.Errorf("initialize encryption: %w", encryptionErr)
		}
		store.SetEncryptor(enc)
	}
	if err = store.FinalizeLegacyAgentAssignments(ctx); err != nil {
		return nil, fmt.Errorf("finalize legacy agent assignments: %w", err)
	}
	return store, nil
}

package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// SQLStore shares transactional queries across the supported SQL dialects.
type SQLStore struct {
	readDB      *sqlPool
	writeDB     *sqlPool
	dbPath      string
	enc         *Encryptor
	rollupMu    sync.Mutex
	beforeClose func() error
	closeOnce   sync.Once
	closeErr    error
}

// SQLiteStore preserves the existing constructor and concrete-type API.
type SQLiteStore = SQLStore

func (s *SQLiteStore) SetEncryptor(enc *Encryptor) {
	s.enc = enc
}

// NewSQLiteStore opens the database with separate read and write pools.
func NewSQLiteStore(path string, maxReadConns int) (*SQLiteStore, error) {
	if maxReadConns <= 0 {
		maxReadConns = runtime.NumCPU()
	}

	// modernc.org/sqlite expects PRAGMAs via the _pragma DSN parameter; the
	// mattn-style _journal_mode/_busy_timeout keys are silently ignored.
	pragmas := "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	// path is a filesystem name, not a caller-supplied SQLite URI. Escape
	// reserved characters so '#' cannot truncate it and '?' cannot add options.
	fileURI := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), OmitHost: true}).String()

	// auto_vacuum is a write operation and must be set before any table is
	// created, so it goes only on the write connection and only takes effect on
	// a fresh database. The read-only pool must not set it.
	writeDB, err := sql.Open("sqlite", fileURI+pragmas+"&_pragma=auto_vacuum(INCREMENTAL)")
	if err != nil {
		return nil, fmt.Errorf("open write db: %w", err)
	}
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)

	// Read pool: multiple connections
	readDB, err := sql.Open("sqlite", fileURI+pragmas+"&mode=ro")
	if err != nil {
		writeDB.Close()
		return nil, fmt.Errorf("open read db: %w", err)
	}
	readDB.SetMaxOpenConns(maxReadConns)
	readDB.SetMaxIdleConns(maxReadConns)

	// Run migrations on write connection
	if err := runMigrations(writeDB); err != nil {
		readDB.Close()
		writeDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	if err := hashLegacyTokens(writeDB); err != nil {
		readDB.Close()
		writeDB.Close()
		return nil, fmt.Errorf("hash legacy tokens: %w", err)
	}

	return &SQLiteStore{readDB: &sqlPool{DB: readDB}, writeDB: &sqlPool{DB: writeDB}, dbPath: path}, nil
}

// hashLegacyTokens is a one-time data migration that rewrites any agent or
// heartbeat token still stored in plaintext as its sha256-hex digest. SQLite
// has no sha256 builtin so this cannot be expressed in SQL.
func hashLegacyTokens(db *sql.DB) error {
	var done string
	err := db.QueryRow("SELECT value FROM meta WHERE key='tokens_hashed'").Scan(&done)
	if err == nil && done == "1" {
		return nil
	}
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("check tokens_hashed: %w", err)
	}

	// Rewrite every token and set the flag in one transaction: if any step
	// fails nothing is committed, so a retry never hashes a row twice.
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, tbl := range []string{"agents", "heartbeats"} {
		rows, err := tx.Query("SELECT id, token FROM " + tbl)
		if err != nil {
			return fmt.Errorf("select %s tokens: %w", tbl, err)
		}
		type tok struct {
			id    int64
			token string
		}
		var pending []tok
		for rows.Next() {
			var t tok
			if err := rows.Scan(&t.id, &t.token); err != nil {
				rows.Close()
				return fmt.Errorf("scan %s token: %w", tbl, err)
			}
			// Hash every legacy row unconditionally: a random 64-hex agent token is
			// indistinguishable from a sha256 digest, so a length/hex check would
			// skip genuine plaintext. The meta 'tokens_hashed' gate, committed in
			// the same transaction, guarantees this runs exactly once.
			pending = append(pending, t)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		for _, t := range pending {
			if _, err := tx.Exec("UPDATE "+tbl+" SET token=? WHERE id=?", sha256Hex(t.token), t.id); err != nil {
				return fmt.Errorf("update %s token: %w", tbl, err)
			}
		}
	}

	if _, err := tx.Exec("INSERT INTO meta (key, value) VALUES ('tokens_hashed', '1') ON CONFLICT(key) DO UPDATE SET value=excluded.value"); err != nil {
		return fmt.Errorf("set tokens_hashed: %w", err)
	}
	return tx.Commit()
}

func runMigrations(db *sql.DB) error {
	var hasSchemaTbl int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_version'`).Scan(&hasSchemaTbl); err != nil {
		return fmt.Errorf("check schema_version table: %w", err)
	}

	if hasSchemaTbl == 0 {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin base schema: %w", err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec(schema); err != nil {
			return fmt.Errorf("apply base schema: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_version (version) VALUES (?)", schemaVersion); err != nil {
			return fmt.Errorf("stamp schema version: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit base schema: %w", err)
		}
		return nil
	}

	var currentVersion int
	if err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&currentVersion); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if currentVersion > schemaVersion {
		return fmt.Errorf("database schema v%d is newer than supported v%d; use a newer Asura binary", currentVersion, schemaVersion)
	}

	if len(migrations) > 0 {
		minRequired := migrations[0].version - 1
		if currentVersion < minRequired {
			return fmt.Errorf("database schema v%d is too old (minimum v%d); upgrade through v1.0.0 first", currentVersion, minRequired)
		}
	}

	for _, m := range migrations {
		if m.version <= currentVersion {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("migration v%d begin: %w", m.version, err)
		}
		if _, err := tx.Exec(m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration v%d: %w", m.version, err)
		}
		if _, err := tx.Exec("UPDATE schema_version SET version = ?", m.version); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration v%d version update: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration v%d commit: %w", m.version, err)
		}
		currentVersion = m.version
	}

	if currentVersion < schemaVersion {
		return fmt.Errorf("database schema v%d is too old (minimum v%d); upgrade through v1.0.0 first", currentVersion, schemaVersion)
	}

	return nil
}

func (s *SQLiteStore) Vacuum(ctx context.Context) error {
	_, err := s.writeDB.ExecContext(ctx, "VACUUM")
	return err
}

// IncrementalVacuum reclaims free pages without rewriting the whole database.
// It only has an effect when the database uses auto_vacuum=INCREMENTAL.
func (s *SQLiteStore) IncrementalVacuum(ctx context.Context) error {
	if s.writeDB.dialect == postgresDialect {
		// PostgreSQL manages dead tuples through autovacuum; it has no SQLite
		// page-reclamation operation to perform on the periodic timer.
		return nil
	}
	_, err := s.writeDB.ExecContext(ctx, "PRAGMA incremental_vacuum")
	return err
}

// EnsureKDFSalt loads the persisted 16-byte PBKDF2 salt, generating and
// storing one on first use. The returned salt is passed to NewEncryptor so the
// derived key is stable across restarts.
func (s *SQLiteStore) EnsureKDFSalt(ctx context.Context) ([]byte, error) {
	var stored string
	err := s.writeDB.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='kdf_salt'").Scan(&stored)
	if err == nil {
		return decodeKDFSalt(stored)
	}
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("read kdf salt: %w", err)
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate kdf salt: %w", err)
	}
	if _, err := s.writeDB.ExecContext(ctx,
		"INSERT INTO meta (key, value) VALUES ('kdf_salt', ?) ON CONFLICT(key) DO NOTHING", hex.EncodeToString(salt)); err != nil {
		return nil, fmt.Errorf("persist kdf salt: %w", err)
	}
	// A concurrent opener may have inserted first. Always return the persisted
	// winner; replacing it would make either process encrypt with a lost key.
	if err := s.writeDB.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='kdf_salt'").Scan(&stored); err != nil {
		return nil, fmt.Errorf("read persisted kdf salt: %w", err)
	}
	return decodeKDFSalt(stored)
}

func decodeKDFSalt(stored string) ([]byte, error) {
	salt, err := hex.DecodeString(stored)
	if err != nil || len(salt) != 16 {
		return nil, fmt.Errorf("persisted encryption salt must be exactly 16 bytes of hexadecimal")
	}
	return salt, nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (s *SQLiteStore) DBSize() (int64, error) {
	if s.readDB.dialect == postgresDialect {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var size int64
		err := s.readDB.QueryRowContext(ctx, "SELECT pg_database_size(current_database())").Scan(&size)
		return size, err
	}
	info, err := os.Stat(s.dbPath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (s *SQLiteStore) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.close() })
	return s.closeErr
}

func (s *SQLiteStore) close() error {
	var firstErr error
	if s.beforeClose != nil {
		firstErr = s.beforeClose()
	}
	if err := s.readDB.Close(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("close read db: %w", err)
	}
	if s.writeDB.dialect == sqliteDialect {
		if _, err := s.writeDB.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("wal checkpoint: %w", err)
		}
	}
	if err := s.writeDB.Close(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("close write db: %w", err)
	}
	return firstErr
}

// timeFormat is the format used for storing timestamps in SQLite.
const timeFormat = "2006-01-02T15:04:05Z"

func formatTime(t time.Time) string {
	return t.UTC().Format(timeFormat)
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(timeFormat, s)
	return t
}

func parseTimePtr(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t := parseTime(s.String)
	return &t
}

// --- Helpers ---

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type scanner interface {
	Scan(dest ...any) error
}

func (s *SQLiteStore) scanMonitor(row scanner) (*Monitor, error) {
	var m Monitor
	var tagsStr, settingsStr, assertionsStr string
	var createdAt, updatedAt string
	var lastCheck sql.NullString
	var groupID, proxyID, escalationPolicyID sql.NullInt64
	err := row.Scan(&m.ID, &m.Name, &m.Description, &m.Type, &m.Target, &m.Interval, &m.Timeout, &m.Enabled,
		&tagsStr, &settingsStr, &assertionsStr, &m.TrackChanges, &m.FailureThreshold, &m.SuccessThreshold,
		&m.UpsideDown, &m.AgentEnabled, &m.ResendInterval, &m.SLATarget, &m.AnomalySensitivity, &groupID, &proxyID, &escalationPolicyID, &createdAt, &updatedAt,
		&m.Status, &lastCheck, &m.ConsecFails, &m.ConsecSuccesses, &m.ConfigRevision)
	if err != nil {
		return nil, err
	}
	if groupID.Valid {
		gid := groupID.Int64
		m.GroupID = &gid
	}
	if proxyID.Valid {
		pid := proxyID.Int64
		m.ProxyID = &pid
	}
	if escalationPolicyID.Valid {
		epid := escalationPolicyID.Int64
		m.EscalationPolicyID = &epid
	}
	m.CreatedAt = parseTime(createdAt)
	m.UpdatedAt = parseTime(updatedAt)
	if err := json.Unmarshal([]byte(tagsStr), &m.Tags); err != nil {
		return nil, fmt.Errorf("decode monitor tags: %w", err)
	}
	settingsStr, err = s.decryptSettings(settingsStr)
	if err != nil {
		return nil, fmt.Errorf("decrypt settings: %w", err)
	}
	m.Settings = json.RawMessage(settingsStr)
	m.Assertions = json.RawMessage(assertionsStr)
	m.LastCheckAt = parseTimePtr(lastCheck)
	if m.Tags == nil {
		m.Tags = []string{}
	}
	if !m.Enabled {
		m.Status = "paused"
	}
	if strings.TrimSpace(settingsStr) == "" {
		m.Settings = json.RawMessage("{}")
	}
	if strings.TrimSpace(assertionsStr) == "" {
		m.Assertions = json.RawMessage("[]")
	}
	return &m, nil
}

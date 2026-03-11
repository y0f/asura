package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteStore implements Store using SQLite with WAL mode.
type SQLiteStore struct {
	readDB  *sql.DB
	writeDB *sql.DB
	dbPath  string
}

// NewSQLiteStore opens the database with separate read and write pools.
func NewSQLiteStore(path string, maxReadConns int) (*SQLiteStore, error) {
	if maxReadConns <= 0 {
		maxReadConns = runtime.NumCPU()
	}

	// Write connection: single connection, WAL mode
	writeDB, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_foreign_keys=ON")
	if err != nil {
		return nil, fmt.Errorf("open write db: %w", err)
	}
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)

	// Read pool: multiple connections
	readDB, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_foreign_keys=ON&mode=ro")
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

	return &SQLiteStore{readDB: readDB, writeDB: writeDB, dbPath: path}, nil
}

func runMigrations(db *sql.DB) error {
	var hasSchemaTbl int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_version'`).Scan(&hasSchemaTbl); err != nil {
		return fmt.Errorf("check schema_version table: %w", err)
	}

	if hasSchemaTbl == 0 {
		if _, err := db.Exec(schema); err != nil {
			return fmt.Errorf("apply base schema: %w", err)
		}
		if _, err := db.Exec("INSERT INTO schema_version (version) VALUES (?)", schemaVersion); err != nil {
			return fmt.Errorf("stamp schema version: %w", err)
		}
		return nil
	}

	var currentVersion int
	if err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&currentVersion); err != nil {
		return fmt.Errorf("read schema version: %w", err)
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

func (s *SQLiteStore) DBSize() (int64, error) {
	info, err := os.Stat(s.dbPath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (s *SQLiteStore) Close() error {
	var firstErr error
	if err := s.readDB.Close(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("close read db: %w", err)
	}
	if _, err := s.writeDB.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("wal checkpoint: %w", err)
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

func scanMonitor(row scanner) (*Monitor, error) {
	var m Monitor
	var tagsStr, settingsStr, assertionsStr string
	var createdAt, updatedAt string
	var lastCheck sql.NullString
	var groupID, proxyID, escalationPolicyID sql.NullInt64
	err := row.Scan(&m.ID, &m.Name, &m.Description, &m.Type, &m.Target, &m.Interval, &m.Timeout, &m.Enabled,
		&tagsStr, &settingsStr, &assertionsStr, &m.TrackChanges, &m.FailureThreshold, &m.SuccessThreshold,
		&m.UpsideDown, &m.ResendInterval, &m.SLATarget, &groupID, &proxyID, &escalationPolicyID, &createdAt, &updatedAt,
		&m.Status, &lastCheck, &m.ConsecFails, &m.ConsecSuccesses)
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
	json.Unmarshal([]byte(tagsStr), &m.Tags)
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

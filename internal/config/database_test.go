package config

import (
	"strings"
	"testing"
)

func TestDatabaseBackendConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, driver, path, dsn string
		valid                   bool
	}{
		{"legacy", "", "asura.db", "", true},
		{"sqlite", "sqlite", "asura.db", "", true},
		{"postgres", "postgres", "", "postgres://user:private-password@localhost/asura", true},
		{"missing path", "sqlite", "", "", false},
		{"wrong dsn", "sqlite", "asura.db", "private-password", false},
		{"missing dsn", "postgres", "asura.db", " ", false},
		{"unknown backend", "private-password", "asura.db", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Database.Driver, cfg.Database.Path, cfg.Database.DSN = tc.driver, tc.path, tc.dsn
			err := cfg.Validate()
			if (err == nil) != tc.valid {
				t.Fatalf("configuration acceptance=%v, want %v: %v", err == nil, tc.valid, err)
			}
			if err != nil && strings.Contains(err.Error(), "private-password") {
				t.Fatal("database configuration error exposed a credential")
			}
		})
	}
}

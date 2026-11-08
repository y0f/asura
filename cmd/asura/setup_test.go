package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/config"
)

// With database.encryption_key set, --setup-totp must store the TOTP secret
// encrypted ("enc:" prefix), like the running server would.
func TestSetupTOTPEncryptsSecret(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "asura.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := "database:\n" +
		"  path: " + filepath.ToSlash(dbPath) + "\n" +
		"  encryption_key: \"test-encryption-key-0123456789abcdef\"\n" +
		"auth:\n" +
		"  api_keys:\n" +
		"    - name: admin\n" +
		"      hash: \"" + config.HashAPIKey("ak_test") + "\"\n" +
		"      super_admin: true\n" +
		"      totp: true\n"
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	// silence stdout (secret/QR output)
	devnull, _ := os.Open(os.DevNull)
	old := os.Stdout
	os.Stdout = devnull
	err := handleTOTPCommands(cfgPath, "admin", "", "", nil)
	os.Stdout = old
	devnull.Close()
	if err != nil {
		t.Fatalf("setup-totp: %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var secret string
	if err := db.QueryRow(`SELECT secret FROM totp_keys WHERE api_key_name = 'admin'`).Scan(&secret); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "enc:") {
		t.Errorf("encryption_key is set but totp_keys.secret is stored in plaintext: %q", secret)
	}
}

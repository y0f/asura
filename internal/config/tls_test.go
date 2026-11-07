package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRequiresBothTLSFiles(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		wantError  bool
	}{
		{"plain HTTP", "server: {}\n", false},
		{"certificate only", "server:\n  tls_cert: server.crt\n", true},
		{"key only", "server:\n  tls_key: server.key\n", true},
		{"certificate and key", "server:\n  tls_cert: server.crt\n  tls_key: server.key\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(file, []byte(tc.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(file)
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "tls_cert") || !strings.Contains(err.Error(), "tls_key") {
					t.Fatalf("incomplete TLS configuration must fail before starting HTTP: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

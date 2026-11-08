package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsNonPositiveWorkerIntervals(t *testing.T) {
	for _, field := range []struct{ section, name string }{
		{"database", "retention_period"},
		{"monitor", "heartbeat_check_interval"},
	} {
		for _, interval := range []string{"0s", "-1s", "1s"} {
			t.Run(field.name+"/"+interval, func(t *testing.T) {
				file := filepath.Join(t.TempDir(), "config.yaml")
				body := fmt.Sprintf("%s:\n  %s: %s\n", field.section, field.name, interval)
				if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				_, err := Load(file)
				if interval == "1s" {
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil || !strings.Contains(err.Error(), field.section+"."+field.name) {
					t.Fatalf("expected actionable interval error, got %v", err)
				}
			})
		}
	}
}

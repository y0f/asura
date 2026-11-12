package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveToken(t *testing.T) {
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	multi := filepath.Join(t.TempDir(), "multi")
	if err := os.WriteFile(multi, []byte("a\nb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, file, env, want string
		ok                    bool
	}{
		{"environment", "", "from-env", "from-env", true},
		{"file wins and is trimmed", file, "from-env", "from-file", true},
		{"missing", "", "", "", false},
		{"blank", "", "  \n", "", false},
		{"unreadable file", filepath.Join(t.TempDir(), "absent"), "from-env", "", false},
		{"multi-line file", multi, "", "", false},
	} {
		got, err := resolveToken(tc.file, tc.env)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("%s: resolveToken = %q, %v; want %q ok=%v", tc.name, got, err, tc.want, tc.ok)
		}
	}
}

// Plain HTTP would send the agent token unencrypted. It is accepted only for
// a loopback server or with an explicit opt-in.
func TestValidateServerURL(t *testing.T) {
	for _, tc := range []struct {
		raw      string
		insecure bool
		want     string
		ok       bool
	}{
		{"https://monitor.example.com/", false, "https://monitor.example.com", true},
		{"https://monitor.example.com/asura", false, "https://monitor.example.com/asura", true},
		{"http://127.0.0.1:8090", false, "http://127.0.0.1:8090", true},
		{"http://[::1]:8090", false, "http://[::1]:8090", true},
		{"http://localhost:8090", false, "http://localhost:8090", true},
		{"http://monitor.example.com", false, "", false},
		{"http://monitor.example.com", true, "http://monitor.example.com", true},
		{"ftp://monitor.example.com", true, "", false},
		{"https://user:pass@monitor.example.com", false, "", false},
		{"https://monitor.example.com/?token=x", false, "", false},
		{"monitor.example.com", false, "", false},
		{"", false, "", false},
	} {
		got, err := validateServerURL(tc.raw, tc.insecure)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("validateServerURL(%q, %v) = %q, %v; want %q ok=%v", tc.raw, tc.insecure, got, err, tc.want, tc.ok)
		}
	}
}

func TestAgentRefusesTokenOnCommandLine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperAgentMain$")
	cmd.Env = append(os.Environ(), "ASURA_TEST_AGENT_MAIN=1", "ASURA_TEST_AGENT_ARGS=-server https://monitor.example.com -token visible-secret")
	out, err := cmd.CombinedOutput()
	if err == nil || ctx.Err() != nil || !strings.Contains(string(out), "--token is not supported") || strings.Contains(string(out), "visible-secret") {
		t.Fatalf("command-line token was not refused cleanly: %v %s", err, out)
	}
}

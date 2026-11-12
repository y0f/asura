package config

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()

	if cfg.Server.Listen != "127.0.0.1:8090" {
		t.Fatalf("expected listen 127.0.0.1:8090, got %s", cfg.Server.Listen)
	}
	if cfg.Monitor.Workers != 10 {
		t.Fatalf("expected 10 workers, got %d", cfg.Monitor.Workers)
	}
	if cfg.Monitor.DefaultInterval != 60*time.Second {
		t.Fatalf("expected 60s interval, got %s", cfg.Monitor.DefaultInterval)
	}
	if cfg.Database.Path != "asura.db" {
		t.Fatalf("expected asura.db, got %s", cfg.Database.Path)
	}
	if cfg.Database.RetentionDays != 90 {
		t.Fatalf("expected 90 retention days, got %d", cfg.Database.RetentionDays)
	}
	if cfg.Logging.Level != "info" {
		t.Fatalf("expected info log level, got %s", cfg.Logging.Level)
	}
}

func TestValidate(t *testing.T) {
	valid := func() *Config {
		return Defaults()
	}

	t.Run("valid defaults", func(t *testing.T) {
		if err := valid().Validate(); err != nil {
			t.Fatal(err)
		}
	})

	tests := []struct {
		name   string
		modify func(*Config)
		errSub string
	}{
		{
			name:   "empty listen",
			modify: func(c *Config) { c.Server.Listen = "" },
			errSub: "server.listen",
		},
		{
			name:   "zero max body size",
			modify: func(c *Config) { c.Server.MaxBodySize = 0 },
			errSub: "max_body_size",
		},
		{
			name:   "negative rate limit",
			modify: func(c *Config) { c.Server.RateLimitPerSec = -1 },
			errSub: "rate_limit_per_sec",
		},
		{
			name:   "zero rate limit burst",
			modify: func(c *Config) { c.Server.RateLimitBurst = 0 },
			errSub: "rate_limit_burst",
		},
		{
			name:   "invalid external URL",
			modify: func(c *Config) { c.Server.ExternalURL = "not-a-url" },
			errSub: "external_url",
		},
		{
			name:   "base path with ..",
			modify: func(c *Config) { c.Server.BasePath = "/foo/../bar" },
			errSub: "base_path",
		},
		{
			name:   "empty database path",
			modify: func(c *Config) { c.Database.Path = "" },
			errSub: "database.path",
		},
		{
			name:   "zero read conns",
			modify: func(c *Config) { c.Database.MaxReadConns = 0 },
			errSub: "max_read_conns",
		},
		{
			name:   "zero retention days",
			modify: func(c *Config) { c.Database.RetentionDays = 0 },
			errSub: "retention_days",
		},
		{
			name:   "zero workers",
			modify: func(c *Config) { c.Monitor.Workers = 0 },
			errSub: "workers",
		},
		{
			name:   "zero default timeout",
			modify: func(c *Config) { c.Monitor.DefaultTimeout = 0 },
			errSub: "default_timeout",
		},
		{
			name:   "interval too small",
			modify: func(c *Config) { c.Monitor.DefaultInterval = 2 * time.Second },
			errSub: "default_interval",
		},
		{
			name:   "zero failure threshold",
			modify: func(c *Config) { c.Monitor.FailureThreshold = 0 },
			errSub: "failure_threshold",
		},
		{
			name:   "zero success threshold",
			modify: func(c *Config) { c.Monitor.SuccessThreshold = 0 },
			errSub: "success_threshold",
		},
		{
			name:   "invalid log level",
			modify: func(c *Config) { c.Logging.Level = "trace" },
			errSub: "logging.level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := valid()
			tt.modify(c)
			err := c.Validate()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.errSub) {
				t.Fatalf("expected error containing %q, got %q", tt.errSub, err.Error())
			}
		})
	}
}

func TestValidateAPIKeys(t *testing.T) {
	t.Run("admin role sets super admin", testValidateAPIKeysAdminRole)
	t.Run("readonly role sets read permissions", testValidateAPIKeysReadonlyRole)
	t.Run("missing name", testValidateAPIKeysMissingName)
	t.Run("missing hash", testValidateAPIKeysMissingHash)
	t.Run("invalid permission", testValidateAPIKeysInvalidPerm)
	t.Run("no perms and no super admin", testValidateAPIKeysNoPerm)
}

func testValidateAPIKeysAdminRole(t *testing.T) {
	keys := []APIKeyConfig{{Name: "admin", Hash: "abc123", Role: "admin"}}
	if err := validateAPIKeys(keys); err != nil {
		t.Fatal(err)
	}
	if !keys[0].SuperAdmin {
		t.Fatal("expected SuperAdmin to be set")
	}
	if keys[0].Role != "" {
		t.Fatalf("expected Role cleared, got %q", keys[0].Role)
	}
}

func testValidateAPIKeysReadonlyRole(t *testing.T) {
	keys := []APIKeyConfig{{Name: "viewer", Hash: "abc123", Role: "readonly"}}
	if err := validateAPIKeys(keys); err != nil {
		t.Fatal(err)
	}
	if len(keys[0].Permissions) == 0 {
		t.Fatal("expected permissions to be set")
	}
	for _, p := range keys[0].Permissions {
		if !strings.HasSuffix(p, ".read") {
			t.Fatalf("expected read-only permission, got %q", p)
		}
	}
}

func testValidateAPIKeysMissingName(t *testing.T) {
	err := validateAPIKeys([]APIKeyConfig{{Name: "", Hash: "abc123", SuperAdmin: true}})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("expected name error, got %v", err)
	}
}

func testValidateAPIKeysMissingHash(t *testing.T) {
	err := validateAPIKeys([]APIKeyConfig{{Name: "test", Hash: "", SuperAdmin: true}})
	if err == nil || !strings.Contains(err.Error(), "hash is required") {
		t.Fatalf("expected hash error, got %v", err)
	}
}

func testValidateAPIKeysInvalidPerm(t *testing.T) {
	err := validateAPIKeys([]APIKeyConfig{{Name: "test", Hash: "abc123", Permissions: []string{"fake.perm"}}})
	if err == nil || !strings.Contains(err.Error(), "invalid permission") {
		t.Fatalf("expected invalid permission error, got %v", err)
	}
}

func testValidateAPIKeysNoPerm(t *testing.T) {
	err := validateAPIKeys([]APIKeyConfig{{Name: "test", Hash: "abc123"}})
	if err == nil || !strings.Contains(err.Error(), "must have super_admin or permissions") {
		t.Fatalf("expected error, got %v", err)
	}
}

func TestValidateLogLevel(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error"} {
		t.Run(level, func(t *testing.T) {
			if err := validateLogLevel(level); err != nil {
				t.Fatal(err)
			}
		})
	}

	t.Run("invalid", func(t *testing.T) {
		if err := validateLogLevel("trace"); err == nil {
			t.Fatal("expected error for invalid level")
		}
	})
}

func TestNormalizeBasePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"/", ""},
		{"foo", "/foo"},
		{"/foo", "/foo"},
		{"/foo/", "/foo"},
		{"  /foo  ", "/foo"},
		{"/foo/bar/", "/foo/bar"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeBasePath(tt.input)
			if got != tt.want {
				t.Fatalf("NormalizeBasePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestHashAPIKey(t *testing.T) {
	h1 := HashAPIKey("test-key")
	h2 := HashAPIKey("test-key")
	if h1 != h2 {
		t.Fatal("expected deterministic hash")
	}
	if len(h1) != 64 {
		t.Fatalf("expected 64 hex chars, got %d", len(h1))
	}

	h3 := HashAPIKey("different-key")
	if h1 == h3 {
		t.Fatal("different keys should produce different hashes")
	}
}

func TestIsWebUIEnabled(t *testing.T) {
	t.Run("nil defaults to true", func(t *testing.T) {
		cfg := Defaults()
		if !cfg.IsWebUIEnabled() {
			t.Fatal("expected true when nil")
		}
	})

	t.Run("explicit false", func(t *testing.T) {
		cfg := Defaults()
		f := false
		cfg.Server.WebUIEnabled = &f
		if cfg.IsWebUIEnabled() {
			t.Fatal("expected false")
		}
	})

	t.Run("explicit true", func(t *testing.T) {
		cfg := Defaults()
		tr := true
		cfg.Server.WebUIEnabled = &tr
		if !cfg.IsWebUIEnabled() {
			t.Fatal("expected true")
		}
	})
}

func TestLookupAPIKey(t *testing.T) {
	cfg := Defaults()
	hash := HashAPIKey("my-secret")
	cfg.Auth.APIKeys = []APIKeyConfig{
		{Name: "admin", Hash: hash, SuperAdmin: true},
	}

	t.Run("matching key", func(t *testing.T) {
		found, ok := cfg.LookupAPIKey("my-secret")
		if !ok || found == nil {
			t.Fatal("expected to find key")
		}
		if found.Name != "admin" {
			t.Fatalf("expected admin, got %s", found.Name)
		}
	})

	t.Run("wrong key", func(t *testing.T) {
		_, ok := cfg.LookupAPIKey("wrong-secret")
		if ok {
			t.Fatal("expected not found")
		}
	})
}

func TestLookupAPIKeyByName(t *testing.T) {
	cfg := Defaults()
	cfg.Auth.APIKeys = []APIKeyConfig{
		{Name: "admin", Hash: "abc", SuperAdmin: true},
		{Name: "viewer", Hash: "def", Permissions: []string{"monitors.read"}},
	}

	t.Run("found", func(t *testing.T) {
		k := cfg.LookupAPIKeyByName("viewer")
		if k == nil {
			t.Fatal("expected to find key")
		}
		if k.Name != "viewer" {
			t.Fatalf("expected viewer, got %s", k.Name)
		}
	})

	t.Run("not found", func(t *testing.T) {
		k := cfg.LookupAPIKeyByName("missing")
		if k != nil {
			t.Fatal("expected nil")
		}
	})
}

func TestIsTrustedProxy(t *testing.T) {
	cfg := Defaults()
	nets, err := parseTrustedProxies([]string{"10.0.0.1", "192.168.1.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	cfg.trustedNets = nets

	t.Run("single IP match", func(t *testing.T) {
		if !cfg.IsTrustedProxy(net.ParseIP("10.0.0.1")) {
			t.Fatal("expected trusted")
		}
	})

	t.Run("CIDR range match", func(t *testing.T) {
		if !cfg.IsTrustedProxy(net.ParseIP("192.168.1.50")) {
			t.Fatal("expected trusted")
		}
	})

	t.Run("not trusted", func(t *testing.T) {
		if cfg.IsTrustedProxy(net.ParseIP("172.16.0.1")) {
			t.Fatal("expected not trusted")
		}
	})
}

func TestResolvedExternalURL(t *testing.T) {
	t.Run("with external URL", func(t *testing.T) {
		cfg := Defaults()
		cfg.Server.ExternalURL = "https://example.com/"
		got := cfg.ResolvedExternalURL()
		if got != "https://example.com" {
			t.Fatalf("expected https://example.com, got %s", got)
		}
	})

	t.Run("without external URL", func(t *testing.T) {
		cfg := Defaults()
		cfg.Server.BasePath = "/app"
		got := cfg.ResolvedExternalURL()
		if got != "http://127.0.0.1:8090/app" {
			t.Fatalf("expected http://127.0.0.1:8090/app, got %s", got)
		}
	})

	t.Run("no base path", func(t *testing.T) {
		cfg := Defaults()
		got := cfg.ResolvedExternalURL()
		if got != "http://127.0.0.1:8090" {
			t.Fatalf("expected http://127.0.0.1:8090, got %s", got)
		}
	})
}

func TestTOTPConfig(t *testing.T) {
	t.Run("defaults to false", func(t *testing.T) {
		cfg := Defaults()
		if cfg.Auth.TOTP.Required {
			t.Fatal("expected TOTP.Required to default to false")
		}
	})

	t.Run("parse from YAML", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		data := `
server:
  listen: "0.0.0.0:8090"
database:
  path: "test.db"
auth:
  api_keys:
    - name: "admin"
      hash: "abc123"
      role: "admin"
      totp: true
    - name: "viewer"
      hash: "def456"
      role: "readonly"
  totp:
    required: true
`
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.Auth.TOTP.Required {
			t.Fatal("expected TOTP.Required true")
		}
		if !cfg.Auth.APIKeys[0].TOTP {
			t.Fatal("expected admin key TOTP true")
		}
		if cfg.Auth.APIKeys[1].TOTP {
			t.Fatal("expected viewer key TOTP false")
		}
	})
}

func TestLoad(t *testing.T) {
	t.Run("valid YAML", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		data := `
server:
  listen: "0.0.0.0:9090"
database:
  path: "test.db"
logging:
  level: "debug"
`
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Server.Listen != "0.0.0.0:9090" {
			t.Fatalf("expected 0.0.0.0:9090, got %s", cfg.Server.Listen)
		}
		if cfg.Database.Path != "test.db" {
			t.Fatalf("expected test.db, got %s", cfg.Database.Path)
		}
	})

	t.Run("env var expansion", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		t.Setenv("ASURA_TEST_PORT", "7777")
		data := `
server:
  listen: "0.0.0.0:${ASURA_TEST_PORT}"
database:
  path: "test.db"
`
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Server.Listen != "0.0.0.0:7777" {
			t.Fatalf("expected 0.0.0.0:7777, got %s", cfg.Server.Listen)
		}
	})

	t.Run("invalid YAML", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(path, []byte("{{invalid"), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if err == nil {
			t.Fatal("expected error for invalid YAML")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := Load("/nonexistent/config.yaml")
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})
}

func TestCheckAPIKeyStrength(t *testing.T) {
	for key, ok := range map[string]bool{
		"short":                                  false,
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": false,
		"abababababababababababababababababab":   false,
		"ak_0f3a9c1d7e2b4f6a8c0e1d3b5f7a9c2e4b6d8f0a1c3e5b7d9f1a3c5e7b9d1f3": true,
		"correct horse battery staple, plus more words":                      true,
	} {
		if err := CheckAPIKeyStrength(key); (err == nil) != ok {
			t.Errorf("CheckAPIKeyStrength(%q) = %v, want ok=%v", key, err, ok)
		}
	}
}

func TestDockerSocketsMustBeCleanAbsolutePaths(t *testing.T) {
	for sockets, ok := range map[string]bool{
		"/var/run/docker.sock":        true,
		"relative/docker.sock":        false,
		"/var/run/../run/docker.sock": false,
		"/var/run/docker.sock/":       false,
	} {
		cfg := Defaults()
		cfg.Auth.APIKeys = []APIKeyConfig{{Name: "admin", Hash: HashAPIKey("k"), SuperAdmin: true}}
		cfg.Monitor.DockerSockets = []string{sockets}
		if err := cfg.Validate(); (err == nil) != ok {
			t.Errorf("docker_sockets %q: Validate() = %v, want ok=%v", sockets, err, ok)
		}
	}
}

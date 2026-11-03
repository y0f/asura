package config

import (
	"os"
	"path/filepath"
	"testing"
)

func loadYAML(t *testing.T, yaml string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

// Regression: duplicate API key names pass validation.
func TestDuplicateAPIKeyNamesRejected(t *testing.T) {
	cfg, err := loadYAML(t, `
auth:
  api_keys:
    - name: ops
      hash: `+HashAPIKey("key-A")+`
      role: readonly
    - name: ops
      hash: `+HashAPIKey("key-B")+`
      role: admin
`)
	if err == nil {
		got := cfg.LookupAPIKeyByName("ops")
		t.Fatalf("config with two api_keys named %q loaded without error; LookupAPIKeyByName returns only the first (super_admin=%v)", "ops", got.SuperAdmin)
	}
}

// Regression: auth.session.lifetime and auth.login rate limits are not validated.
func TestAuthLifetimeAndLoginLimitsValidated(t *testing.T) {
	cases := map[string]string{
		"session.lifetime 0s": `
auth:
  session:
    lifetime: 0s
`,
		"session.lifetime negative": `
auth:
  session:
    lifetime: -1h
`,
		"login.rate_limit_burst 0": `
auth:
  login:
    rate_limit_burst: 0
`,
		"login.rate_limit_per_sec 0": `
auth:
  login:
    rate_limit_per_sec: 0
`,
	}
	for name, y := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadYAML(t, y); err == nil {
				t.Fatalf("%s loaded without a validation error", name)
			}
		})
	}
}

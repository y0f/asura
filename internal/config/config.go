package config

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server        ServerConfig        `yaml:"server"`
	Database      DatabaseConfig      `yaml:"database"`
	Auth          AuthConfig          `yaml:"auth"`
	Monitor       MonitorConfig       `yaml:"monitor"`
	Logging       LoggingConfig       `yaml:"logging"`
	Subscriptions SubscriptionsConfig `yaml:"subscriptions"`

	trustedNets []net.IPNet
}

type SubscriptionsConfig struct {
	Enabled bool       `yaml:"enabled"`
	SMTP    SMTPConfig `yaml:"smtp"`
}

type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	From     string `yaml:"from"`
	TLSMode  string `yaml:"tls_mode"`
}

type ServerConfig struct {
	Listen          string        `yaml:"listen"`
	TLSCert         string        `yaml:"tls_cert"`
	TLSKey          string        `yaml:"tls_key"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"`
	IdleTimeout     time.Duration `yaml:"idle_timeout"`
	MaxBodySize     int64         `yaml:"max_body_size"`
	CORSOrigins     []string      `yaml:"cors_origins"`
	RateLimitPerSec float64       `yaml:"rate_limit_per_sec"`
	RateLimitBurst  int           `yaml:"rate_limit_burst"`
	WebUIEnabled    *bool         `yaml:"web_ui_enabled"`
	FrameAncestors  []string      `yaml:"frame_ancestors"`
	BasePath        string        `yaml:"base_path"`
	ExternalURL     string        `yaml:"external_url"`
	TrustedProxies  []string      `yaml:"trusted_proxies"`
}

type DatabaseConfig struct {
	Path                    string        `yaml:"path"`
	MaxReadConns            int           `yaml:"max_read_conns"`
	RetentionDays           int           `yaml:"retention_days"`
	RetentionPeriod         time.Duration `yaml:"retention_period"`
	RequestLogRetentionDays int           `yaml:"request_log_retention_days"`
}

type AuthConfig struct {
	APIKeys []APIKeyConfig `yaml:"api_keys"`
	Session SessionConfig  `yaml:"session"`
	Login   LoginConfig    `yaml:"login"`
	TOTP    TOTPConfig     `yaml:"totp"`
}

type TOTPConfig struct {
	Required bool `yaml:"required"`
}

type SessionConfig struct {
	Lifetime     time.Duration `yaml:"lifetime"`
	CookieSecure bool          `yaml:"cookie_secure"`
}

type LoginConfig struct {
	RateLimitPerSec float64 `yaml:"rate_limit_per_sec"`
	RateLimitBurst  int     `yaml:"rate_limit_burst"`
}

type APIKeyConfig struct {
	Name        string   `yaml:"name"`
	Hash        string   `yaml:"hash"`
	Role        string   `yaml:"role,omitempty"`
	SuperAdmin  bool     `yaml:"super_admin,omitempty"`
	Permissions []string `yaml:"permissions,omitempty"`
	TOTP        bool     `yaml:"totp,omitempty"`
}

var AllPermissions = []string{
	"monitors.read", "monitors.write",
	"incidents.read", "incidents.write",
	"notifications.read", "notifications.write",
	"escalation_policies.read", "escalation_policies.write",
	"maintenance.read", "maintenance.write",
	"metrics.read",
}

func (k *APIKeyConfig) HasPermission(perm string) bool {
	if k.SuperAdmin {
		return true
	}
	for _, p := range k.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

func (k *APIKeyConfig) PermissionMap() map[string]bool {
	m := make(map[string]bool)
	if k.SuperAdmin {
		for _, p := range AllPermissions {
			m[p] = true
		}
		return m
	}
	for _, p := range k.Permissions {
		m[p] = true
	}
	return m
}

type MonitorConfig struct {
	Workers                int           `yaml:"workers"`
	DefaultTimeout         time.Duration `yaml:"default_timeout"`
	DefaultInterval        time.Duration `yaml:"default_interval"`
	FailureThreshold       int           `yaml:"failure_threshold"`
	SuccessThreshold       int           `yaml:"success_threshold"`
	MaxConcurrentDNS       int           `yaml:"max_concurrent_dns"`
	CommandTimeout         time.Duration `yaml:"command_timeout"`
	CommandAllowlist       []string      `yaml:"command_allowlist"`
	HeartbeatCheckInterval time.Duration `yaml:"heartbeat_check_interval"`
	AllowPrivateTargets    bool          `yaml:"allow_private_targets"`
	AdaptiveIntervals      bool          `yaml:"adaptive_intervals"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"` // "text" or "json"
}

func Defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Listen:          "127.0.0.1:8090",
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    30 * time.Second,
			IdleTimeout:     120 * time.Second,
			MaxBodySize:     1 << 20, // 1MB
			RateLimitPerSec: 30,
			RateLimitBurst:  60,
		},
		Database: DatabaseConfig{
			Path:                    "asura.db",
			MaxReadConns:            4,
			RetentionDays:           90,
			RetentionPeriod:         1 * time.Hour,
			RequestLogRetentionDays: 7,
		},
		Auth: AuthConfig{
			Session: SessionConfig{
				Lifetime:     24 * time.Hour,
				CookieSecure: true,
			},
			Login: LoginConfig{
				RateLimitPerSec: 0.2,
				RateLimitBurst:  5,
			},
		},
		Monitor: MonitorConfig{
			Workers:                10,
			DefaultTimeout:         10 * time.Second,
			DefaultInterval:        60 * time.Second,
			FailureThreshold:       3,
			SuccessThreshold:       1,
			CommandTimeout:         30 * time.Second,
			HeartbeatCheckInterval: 30 * time.Second,
			AdaptiveIntervals:      true,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
		},
	}
}

func Load(path string) (*Config, error) {
	cfg := Defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Expand environment variables
	expanded := os.ExpandEnv(string(data))

	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	cfg.Server.BasePath = NormalizeBasePath(cfg.Server.BasePath)

	nets, err := parseTrustedProxies(cfg.Server.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("parse trusted_proxies: %w", err)
	}
	cfg.trustedNets = nets

	return cfg, nil
}

func (c *Config) Validate() error {
	if err := c.validateServer(); err != nil {
		return err
	}
	if err := c.validateDatabase(); err != nil {
		return err
	}
	if err := c.validateMonitorConfig(); err != nil {
		return err
	}
	if err := validateAPIKeys(c.Auth.APIKeys); err != nil {
		return err
	}
	return validateLogLevel(c.Logging.Level)
}

func (c *Config) validateServer() error {
	if c.Server.Listen == "" {
		return fmt.Errorf("server.listen is required")
	}
	if c.Server.MaxBodySize <= 0 {
		return fmt.Errorf("server.max_body_size must be positive")
	}
	if c.Server.RateLimitPerSec <= 0 {
		return fmt.Errorf("server.rate_limit_per_sec must be positive")
	}
	if c.Server.RateLimitBurst <= 0 {
		return fmt.Errorf("server.rate_limit_burst must be positive")
	}
	if c.Server.ExternalURL != "" {
		u, err := url.Parse(c.Server.ExternalURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("server.external_url must be an absolute URL (e.g. https://example.com)")
		}
	}
	if bp := c.Server.BasePath; bp != "" {
		if strings.Contains(bp, "..") || strings.Contains(bp, "?") || strings.Contains(bp, "#") || strings.Contains(bp, "\\") {
			return fmt.Errorf("server.base_path contains invalid characters")
		}
	}
	return nil
}

func (c *Config) validateDatabase() error {
	if c.Database.Path == "" {
		return fmt.Errorf("database.path is required")
	}
	if c.Database.MaxReadConns <= 0 {
		return fmt.Errorf("database.max_read_conns must be positive")
	}
	if c.Database.RetentionDays <= 0 {
		return fmt.Errorf("database.retention_days must be positive")
	}
	return nil
}

func (c *Config) validateMonitorConfig() error {
	if c.Monitor.Workers <= 0 {
		return fmt.Errorf("monitor.workers must be positive")
	}
	if c.Monitor.DefaultTimeout <= 0 {
		return fmt.Errorf("monitor.default_timeout must be positive")
	}
	if c.Monitor.DefaultInterval < 5*time.Second {
		return fmt.Errorf("monitor.default_interval must be at least 5s")
	}
	if c.Monitor.FailureThreshold <= 0 {
		return fmt.Errorf("monitor.failure_threshold must be positive")
	}
	if c.Monitor.SuccessThreshold <= 0 {
		return fmt.Errorf("monitor.success_threshold must be positive")
	}
	return nil
}

func validateAPIKeys(keys []APIKeyConfig) error {
	validPerms := make(map[string]bool)
	for _, p := range AllPermissions {
		validPerms[p] = true
	}

	for i := range keys {
		key := &keys[i]
		if key.Name == "" {
			return fmt.Errorf("auth.api_keys[%d].name is required", i)
		}
		if key.Hash == "" {
			return fmt.Errorf("auth.api_keys[%d].hash is required", i)
		}
		if key.Role == "admin" {
			key.SuperAdmin = true
			key.Role = ""
		} else if key.Role == "readonly" {
			key.Permissions = []string{
				"monitors.read", "incidents.read",
				"notifications.read", "escalation_policies.read",
				"maintenance.read", "metrics.read",
			}
			key.Role = ""
		}
		if !key.SuperAdmin && len(key.Permissions) == 0 {
			return fmt.Errorf("auth.api_keys[%d] must have super_admin or permissions", i)
		}
		for _, p := range key.Permissions {
			if !validPerms[p] {
				return fmt.Errorf("auth.api_keys[%d] invalid permission: %s", i, p)
			}
		}
	}
	return nil
}

func validateLogLevel(level string) error {
	switch strings.ToLower(level) {
	case "debug", "info", "warn", "error":
		return nil
	default:
		return fmt.Errorf("logging.level must be one of: debug, info, warn, error")
	}
}

func HashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

func (c *Config) IsWebUIEnabled() bool {
	if c.Server.WebUIEnabled == nil {
		return true
	}
	return *c.Server.WebUIEnabled
}

// LookupAPIKeyByName finds an API key config by its name.
func (c *Config) LookupAPIKeyByName(name string) *APIKeyConfig {
	for i := range c.Auth.APIKeys {
		if c.Auth.APIKeys[i].Name == name {
			return &c.Auth.APIKeys[i]
		}
	}
	return nil
}

// LookupAPIKey checks if the given key matches any configured API key
// and returns the key config if found.
func (c *Config) LookupAPIKey(key string) (*APIKeyConfig, bool) {
	hash := HashAPIKey(key)
	for i := range c.Auth.APIKeys {
		if subtle.ConstantTimeCompare([]byte(c.Auth.APIKeys[i].Hash), []byte(hash)) == 1 {
			return &c.Auth.APIKeys[i], true
		}
	}
	return nil, false
}

func NormalizeBasePath(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "/" {
		return ""
	}
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return strings.TrimRight(s, "/")
}

func (c *Config) IsTrustedProxy(ip net.IP) bool {
	for i := range c.trustedNets {
		if c.trustedNets[i].Contains(ip) {
			return true
		}
	}
	return false
}

func (c *Config) TrustedNets() []net.IPNet {
	return c.trustedNets
}

func (c *Config) ResolvedExternalURL() string {
	if c.Server.ExternalURL != "" {
		return strings.TrimRight(c.Server.ExternalURL, "/")
	}
	return "http://" + c.Server.Listen + c.Server.BasePath
}

func parseTrustedProxies(proxies []string) ([]net.IPNet, error) {
	var nets []net.IPNet
	for _, p := range proxies {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.Contains(p, "/") {
			ip := net.ParseIP(p)
			if ip == nil {
				return nil, fmt.Errorf("invalid IP: %s", p)
			}
			if ip.To4() != nil {
				p += "/32"
			} else {
				p += "/128"
			}
		}
		_, ipNet, err := net.ParseCIDR(p)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR: %s", p)
		}
		nets = append(nets, *ipNet)
	}
	return nets, nil
}

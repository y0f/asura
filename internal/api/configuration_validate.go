package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/y0f/asura/internal/assertion"
	"github.com/y0f/asura/internal/notifier"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
)

// MaxConfigurationBytes bounds both JSON and multipart imports. The normal
// small request limit does not apply to these authenticated transfer routes.
const MaxConfigurationBytes int64 = 32 << 20

func validateImportedConfiguration(c *storage.ConfigurationSnapshot) error {
	if len(c.Monitors)+len(c.Groups)+len(c.Tags)+len(c.Proxies)+len(c.Channels)+len(c.EscalationPolicies)+len(c.Rotations)+len(c.Maintenance)+len(c.StatusPages)+len(c.Agents) > 100000 {
		return fmt.Errorf("configuration exceeds 100000 entities")
	}
	for _, m := range c.Monitors {
		if m == nil {
			return fmt.Errorf("monitor must not be null")
		}
		if err := validateImportedMonitor(m); err != nil {
			return fmt.Errorf("monitor %d: %w", m.ID, err)
		}
	}
	for _, g := range c.Groups {
		if g == nil {
			return fmt.Errorf("group must not be null")
		}
		if err := validate.ValidateMonitorGroup(g); err != nil {
			return fmt.Errorf("group %d: %w", g.ID, err)
		}
	}
	for _, tag := range c.Tags {
		if tag == nil {
			return fmt.Errorf("tag must not be null")
		}
		if err := validate.ValidateTag(tag); err != nil {
			return fmt.Errorf("tag %d: %w", tag.ID, err)
		}
	}
	for _, proxy := range c.Proxies {
		if proxy == nil {
			return fmt.Errorf("proxy must not be null")
		}
		if err := validate.ValidateProxy(proxy); err != nil {
			return fmt.Errorf("proxy %d: %w", proxy.ID, err)
		}
		if len(proxy.AuthUser) > 4096 || len(proxy.AuthPass) > 16384 || strings.HasPrefix(proxy.AuthPass, "enc:") {
			return fmt.Errorf("proxy %d credentials must be plaintext and within size limits", proxy.ID)
		}
	}
	for _, ch := range c.Channels {
		if ch == nil {
			return fmt.Errorf("notification channel must not be null")
		}
		if err := validate.ValidateNotificationChannel(ch); err != nil {
			return fmt.Errorf("channel %d: invalid name, type, settings or events", ch.ID)
		}
		if err := validateImportedChannel(ch); err != nil {
			return fmt.Errorf("channel %d: %w", ch.ID, err)
		}
	}
	for _, policy := range c.EscalationPolicies {
		if policy == nil || len(policy.Steps) > 1000 {
			return fmt.Errorf("escalation policy must have at most 1000 steps")
		}
		for _, step := range policy.Steps {
			if step == nil || step.DelayMinutes < 0 || int64(step.DelayMinutes) > math.MaxInt64/int64(time.Minute) {
				return fmt.Errorf("policy %d has an invalid step delay", policy.ID)
			}
		}
		if err := validate.ValidateEscalationPolicy(policy); err != nil {
			return fmt.Errorf("policy %d: %w", policy.ID, err)
		}
	}
	for _, r := range c.Rotations {
		if r == nil || len(r.Name) > 255 || r.PeriodStartedAt.IsZero() || r.PeriodStartedAt.Year() < 1 || r.PeriodStartedAt.Year() > 9999 {
			return fmt.Errorf("rotation requires a valid name and period_started_at")
		}
	}
	for _, mw := range c.Maintenance {
		if mw == nil || len(mw.Name) > 255 || len(mw.CronExpr) > 255 {
			return fmt.Errorf("maintenance name or cron expression exceeds limits")
		}
		if err := validate.ValidateMaintenanceWindow(mw); err != nil {
			return fmt.Errorf("maintenance %d: %w", mw.ID, err)
		}
	}
	for _, p := range c.StatusPages {
		if p == nil {
			return fmt.Errorf("status page must not be null")
		}
		original := *p
		if err := validate.ValidateStatusPage(p); err != nil {
			return fmt.Errorf("status page %d: %w", p.ID, err)
		}
		// Imported protection/branding must not silently change through slug
		// substitution or markup sanitization. Existing safe exports are stable.
		if p.Slug != original.Slug || p.CustomCSS != original.CustomCSS || p.CustomHeaderHTML != original.CustomHeaderHTML || p.AnalyticsScript != original.AnalyticsScript {
			return fmt.Errorf("status page %d contains an invalid slug or unsafe/noncanonical markup; save it through the status page editor first", p.ID)
		}
		if p.PasswordHash != "" {
			decoded, err := hex.DecodeString(p.PasswordHash)
			if err != nil || len(decoded) != 32 || strings.ToLower(p.PasswordHash) != p.PasswordHash {
				return fmt.Errorf("status page %d password_hash must be a lowercase SHA-256 digest", p.ID)
			}
		}
	}
	for _, relation := range c.StatusPageMonitors {
		if len(relation.GroupName) > 255 {
			return fmt.Errorf("status page monitor group name exceeds 255 characters")
		}
	}
	for _, agent := range c.Agents {
		if strings.TrimSpace(agent.Name) == "" || len(agent.Name) > 255 {
			return fmt.Errorf("agent declaration requires a name of at most 255 characters")
		}
	}
	return nil
}

func validateImportedMonitor(m *storage.Monitor) error {
	if err := validate.ValidateMonitor(m); err != nil {
		return err
	}
	if m.Interval < 5 || m.Interval > 86400 || m.Timeout < 1 || m.Timeout > 300 || m.FailureThreshold < 1 || m.SuccessThreshold < 1 || m.ResendInterval < 0 || m.ResendInterval > 86400 {
		return fmt.Errorf("invalid interval, timeout or alert thresholds")
	}
	if math.IsNaN(m.SLATarget) || math.IsInf(m.SLATarget, 0) || m.SLATarget < 0 || m.SLATarget > 100 {
		return fmt.Errorf("sla_target must be finite and between 0 and 100")
	}
	if !slices.Contains([]string{"", "off", "low", "medium", "high"}, m.AnomalySensitivity) {
		return fmt.Errorf("invalid anomaly_sensitivity")
	}
	if len(m.Target) > 2048 || len(m.Tags) > 1000 || len(m.Settings) > 1<<20 || len(m.Assertions) > 1<<20 {
		return fmt.Errorf("target, tags, settings or assertions exceed size limits")
	}
	if err := validate.ValidateMonitorTags(m.MonitorTags); err != nil {
		return err
	}
	if err := validateImportedAssertions(m.Assertions); err != nil {
		return err
	}
	var settings any
	switch m.Type {
	case "http":
		settings = new(storage.HTTPSettings)
	case "http_multi":
		settings = new(storage.MultiStepHTTPSettings)
	case "tcp":
		settings = new(storage.TCPSettings)
	case "udp":
		settings = new(storage.UDPSettings)
	case "dns":
		settings = new(storage.DNSSettings)
	case "tls":
		settings = new(storage.TLSSettings)
	case "websocket":
		settings = new(storage.WebSocketSettings)
	case "command":
		settings = new(storage.CommandSettings)
	case "docker":
		settings = new(storage.DockerSettings)
	case "domain":
		settings = new(storage.DomainSettings)
	case "grpc":
		settings = new(storage.GRPCSettings)
	case "mqtt":
		settings = new(storage.MQTTSettings)
	case "smtp":
		settings = new(storage.SMTPSettings)
	case "ssh":
		settings = new(storage.SSHSettings)
	case "redis":
		settings = new(storage.RedisSettings)
	case "postgresql":
		settings = new(storage.PostgreSQLSettings)
	case "heartbeat":
		settings = new(struct {
			Grace int `json:"grace,omitempty"`
		})
	default:
		settings = new(struct{})
	}
	if err := decodeConfigurationSettings(m.Settings, settings); err != nil {
		return err
	}
	switch s := settings.(type) {
	case *storage.HTTPSettings:
		if _, err := http.NewRequest(s.Method, m.Target, nil); err != nil || !configurationURL(m.Target, "http", "https") {
			return fmt.Errorf("HTTP target and method must form a valid HTTP(S) request")
		}
		if s.ExpectedStatus < 0 || s.ExpectedStatus > 599 || s.ExpectedStatus > 0 && s.ExpectedStatus < 100 || s.MaxRedirects < 0 || s.MaxRedirects > 100 {
			return fmt.Errorf("invalid HTTP status or redirect limit")
		}
		if !slices.Contains([]string{"", "none", "basic", "bearer", "oauth2"}, s.AuthMethod) || !slices.Contains([]string{"", "json", "xml", "form", "raw"}, s.BodyEncoding) {
			return fmt.Errorf("invalid HTTP authentication or body encoding")
		}
	case *storage.MultiStepHTTPSettings:
		if len(s.Steps) == 0 || len(s.Steps) > 5 {
			return fmt.Errorf("multi-step HTTP requires 1 to 5 steps")
		}
		variables := map[string]bool{}
		for _, step := range s.Steps {
			if _, err := http.NewRequest(step.Method, "https://example.test", nil); err != nil || !configurationStepURL(step.URL, variables) {
				return fmt.Errorf("multi-step HTTP requires valid HTTP(S) URLs and methods")
			}
			if step.ExpectedStatus < 0 || step.ExpectedStatus > 599 || step.ExpectedStatus > 0 && step.ExpectedStatus < 100 {
				return fmt.Errorf("invalid step expected_status")
			}
			if step.ExtractRegex != "" {
				if _, err := regexp.Compile(step.ExtractRegex); err != nil {
					return fmt.Errorf("invalid step extraction expression")
				}
			}
			if step.ExtractVar != "" {
				variables[step.ExtractVar] = true
			}
		}
	case *storage.DNSSettings:
		if !slices.Contains([]string{"", "A", "AAAA", "CNAME", "MX", "TXT", "NS"}, s.RecordType) {
			return fmt.Errorf("unsupported DNS record type")
		}
	case *storage.TLSSettings:
		if s.WarnDaysBefore < 0 || s.WarnDaysBefore > 36500 {
			return fmt.Errorf("invalid TLS expiry warning days")
		}
	case *storage.DomainSettings:
		if s.WarnDaysBefore < 0 || s.WarnDaysBefore > 36500 {
			return fmt.Errorf("invalid domain expiry warning days")
		}
	case *storage.SMTPSettings:
		if s.Port < 0 || s.Port > 65535 {
			return fmt.Errorf("invalid SMTP port")
		}
	case *storage.WebSocketSettings:
		if !configurationURL(m.Target, "ws", "wss") {
			return fmt.Errorf("websocket target must use ws or wss")
		}
	case *storage.CommandSettings:
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("command settings require a command")
		}
	}
	return nil
}

func configurationStepURL(raw string, variables map[string]bool) bool {
	if len(raw) > 2048 || strings.ContainsAny(raw, "\r\n") {
		return false
	}
	remaining, dynamic := raw, false
	for {
		start := strings.Index(remaining, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(remaining[start+2:], "}}")
		if end < 0 || !variables[remaining[start+2:start+2+end]] {
			return false
		}
		dynamic = true
		remaining = remaining[start+2+end+2:]
	}
	// An earlier response may supply an entire base URL or a path/query value.
	// Its eventual value cannot be verified without performing the checks.
	return dynamic || configurationURL(raw, "http", "https")
}

func decodeConfigurationSettings(raw json.RawMessage, value any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 1<<20 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return fmt.Errorf("settings must be a JSON object of at most 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("settings contain an unknown field or invalid field type")
	}
	return nil
}

func configurationURL(raw string, schemes ...string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Hostname() != "" && slices.Contains(schemes, u.Scheme)
}

func validateImportedAssertions(raw json.RawMessage) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "[]" || trimmed == "{}" || trimmed == "null" {
		return nil
	}
	var set assertion.ConditionSet
	if err := decodeConfigurationSettings(raw, &set); err != nil {
		return fmt.Errorf("assertions must be a valid condition-set object")
	}
	validLogic := func(op string) bool { return op == "" || op == "and" || op == "or" }
	if !validLogic(set.Operator) || len(set.Groups) > 100 {
		return fmt.Errorf("assertion groups/operator exceed supported limits")
	}
	for _, group := range set.Groups {
		if !validLogic(group.Operator) || len(group.Conditions) > 100 {
			return fmt.Errorf("assertion conditions/operator exceed supported limits")
		}
		for _, condition := range group.Conditions {
			operators := []string{"", "eq", "neq", "gt", "lt", "gte", "lte"}
			switch condition.Type {
			case "status_code", "response_time", "cert_expiry":
				if _, err := strconv.ParseInt(condition.Value, 10, 64); err != nil {
					return fmt.Errorf("numeric assertion requires an integer value")
				}
			case "body_contains":
				operators = []string{"", "contains", "not_contains"}
			case "body_regex":
				operators = []string{"", "matches", "not_matches"}
				if _, err := regexp.Compile(condition.Value); err != nil {
					return fmt.Errorf("invalid assertion regular expression")
				}
			case "header", "json_path":
				operators = append(operators, "contains", "not_contains", "exists")
				if condition.Target == "" {
					return fmt.Errorf("header/JSON assertion target is required")
				}
			case "dns_record":
				operators = []string{"", "contains", "eq"}
			default:
				return fmt.Errorf("unknown assertion type")
			}
			if !slices.Contains(operators, condition.Operator) {
				return fmt.Errorf("unsupported assertion operator")
			}
		}
	}
	return nil
}

func validateImportedChannel(ch *storage.NotificationChannel) error {
	var settings any
	switch ch.Type {
	case "webhook":
		settings = new(notifier.WebhookSettings)
	case "email":
		settings = new(notifier.EmailSettings)
	case "telegram":
		settings = new(notifier.TelegramSettings)
	case "discord":
		settings = new(notifier.DiscordSettings)
	case "slack":
		settings = new(notifier.SlackSettings)
	case "ntfy":
		settings = new(notifier.NtfySettings)
	case "teams":
		settings = new(notifier.TeamsSettings)
	case "pagerduty":
		settings = new(notifier.PagerDutySettings)
	case "opsgenie":
		settings = new(notifier.OpsgenieSettings)
	case "pushover":
		settings = new(notifier.PushoverSettings)
	case "googlechat":
		settings = new(notifier.GoogleChatSettings)
	case "matrix":
		settings = new(notifier.MatrixSettings)
	case "gotify":
		settings = new(notifier.GotifySettings)
	default:
		return fmt.Errorf("unsupported notification type")
	}
	if err := decodeConfigurationSettings(ch.Settings, settings); err != nil {
		return err
	}
	valid := true
	webURL := func(value string) bool { return configurationURL(value, "http", "https") }
	switch s := settings.(type) {
	case *notifier.WebhookSettings:
		valid = webURL(s.URL)
	case *notifier.DiscordSettings:
		valid = webURL(s.WebhookURL)
	case *notifier.SlackSettings:
		valid = webURL(s.WebhookURL)
	case *notifier.TeamsSettings:
		valid = webURL(s.WebhookURL)
	case *notifier.GoogleChatSettings:
		valid = webURL(s.WebhookURL)
	case *notifier.TelegramSettings:
		valid = s.BotToken != "" && s.ChatID != ""
	case *notifier.PagerDutySettings:
		valid = s.RoutingKey != ""
	case *notifier.OpsgenieSettings:
		valid = s.APIKey != "" && slices.Contains([]string{"", "us", "eu"}, s.Region)
	case *notifier.PushoverSettings:
		valid = s.UserKey != "" && s.AppToken != "" && s.Priority >= -2 && s.Priority <= 2
	case *notifier.MatrixSettings:
		valid = webURL(s.Homeserver) && s.AccessToken != "" && s.RoomID != ""
	case *notifier.GotifySettings:
		valid = webURL(s.ServerURL) && s.AppToken != "" && s.Priority >= 0
	case *notifier.NtfySettings:
		valid = (s.ServerURL == "" || webURL(s.ServerURL)) && s.Topic != "" && s.Priority >= 0 && s.Priority <= 5
	case *notifier.EmailSettings:
		valid = s.Host != "" && s.Port >= 0 && s.Port <= 65535 && len(s.To) > 0 && len(s.To)+len(s.CC)+len(s.BCC) <= 1000 && slices.Contains([]string{"", "none", "starttls", "starttls_required", "smtps"}, s.TLSMode)
		for _, addresses := range [][]string{{s.From}, s.To, s.CC, s.BCC} {
			for _, address := range addresses {
				if _, err := mail.ParseAddress(address); err != nil {
					valid = false
				}
			}
		}
	}
	if !valid {
		return fmt.Errorf("notification settings are incomplete or invalid for this type")
	}
	return nil
}

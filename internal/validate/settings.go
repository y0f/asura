package validate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/y0f/asura/v2/internal/assertion"
	"github.com/y0f/asura/v2/internal/jsonutil"
	"github.com/y0f/asura/v2/internal/notifier"
	"github.com/y0f/asura/v2/internal/storage"
)

type heartbeatSettings struct {
	Grace int `json:"grace,omitempty"`
}

func validateMonitorConfiguration(m *storage.Monitor) error {
	if !slices.Contains([]string{"", "off", "low", "medium", "high"}, m.AnomalySensitivity) {
		return fmt.Errorf("invalid anomaly_sensitivity")
	}
	if len(m.Target) > 2048 || len(m.Tags) > 1000 || len(m.Settings) > 1<<20 || len(m.Assertions) > 1<<20 {
		return fmt.Errorf("target, tags, settings or assertions exceed size limits")
	}
	if err := ValidateMonitorTags(m.MonitorTags); err != nil {
		return err
	}
	if err := validateAssertions(m.Assertions); err != nil {
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
		settings = new(heartbeatSettings)
	default:
		settings = new(struct{})
	}
	if err := decodeSettings(m.Settings, settings); err != nil {
		return err
	}
	switch s := settings.(type) {
	case *storage.HTTPSettings:
		if _, err := http.NewRequest(s.Method, m.Target, nil); err != nil || !validURL(m.Target, "http", "https") {
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
			if _, err := http.NewRequest(step.Method, "https://example.test", nil); err != nil || !validStepURL(step.URL, variables) {
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
		if !validURL(m.Target, "ws", "wss") {
			return fmt.Errorf("websocket target must use ws or wss")
		}
	case *storage.CommandSettings:
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("command settings require a command")
		}
	case *heartbeatSettings:
		if s.Grace < 0 || s.Grace > 86400 {
			return fmt.Errorf("heartbeat grace must be between 0 and 86400 seconds")
		}
	}
	return nil
}

func validStepURL(raw string, variables map[string]bool) bool {
	if len(raw) > 2048 || strings.ContainsAny(raw, "\r\n") {
		return false
	}
	remaining := raw
	var substituted strings.Builder
	for {
		start := strings.Index(remaining, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(remaining[start+2:], "}}")
		if end < 0 || !variables[remaining[start+2:start+2+end]] {
			return false
		}
		substituted.WriteString(remaining[:start])
		substituted.WriteString("value")
		remaining = remaining[start+2+end+2:]
	}
	substituted.WriteString(remaining)
	// An earlier response may supply an entire URL. Otherwise the literal
	// scheme and host must already form an HTTP(S) URL; a placeholder in a
	// path or query cannot make an unsupported literal scheme acceptable.
	return strings.HasPrefix(raw, "{{") || validURL(substituted.String(), "http", "https")
}

func decodeSettings(raw json.RawMessage, value any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	return jsonutil.DecodeObject(raw, value, 1<<20)
}

func validURL(raw string, schemes ...string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Hostname() != "" && slices.Contains(schemes, u.Scheme)
}

func validateAssertions(raw json.RawMessage) error {
	_, err := assertion.Parse(raw)
	return err
}

func validateNotificationSettings(ch *storage.NotificationChannel) error {
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
	if err := decodeSettings(ch.Settings, settings); err != nil {
		return err
	}
	valid := true
	webURL := func(value string) bool { return validURL(value, "http", "https") }
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

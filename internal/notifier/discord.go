package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/y0f/asura/internal/storage"
)

type DiscordSettings struct {
	WebhookURL string `json:"webhook_url"`
}

type DiscordSender struct{}

func (s *DiscordSender) Type() string { return "discord" }

func (s *DiscordSender) Send(ctx context.Context, channel *storage.NotificationChannel, payload *Payload) error {
	var settings DiscordSettings
	if err := json.Unmarshal(channel.Settings, &settings); err != nil {
		return fmt.Errorf("invalid discord settings: %w", err)
	}

	if settings.WebhookURL == "" {
		return fmt.Errorf("discord webhook_url is required")
	}

	title, description, color := formatDiscordEmbed(payload)

	var fields []map[string]any
	if payload.Incident != nil {
		fields = append(fields, map[string]any{"name": "Monitor", "value": payload.Incident.MonitorName, "inline": true})
		fields = append(fields, map[string]any{"name": "Severity", "value": payload.Incident.Severity, "inline": true})
		if payload.Incident.Cause != "" {
			fields = append(fields, map[string]any{"name": "Cause", "value": payload.Incident.Cause, "inline": false})
		}
	} else if payload.Monitor != nil {
		fields = append(fields, map[string]any{"name": "Monitor", "value": payload.Monitor.Name, "inline": true})
	}

	embed := map[string]any{
		"title":       title,
		"description": description,
		"color":       color,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	}
	if len(fields) > 0 {
		embed["fields"] = fields
	}

	body, _ := json.Marshal(map[string]any{
		"username": "Asura",
		"embeds":   []map[string]any{embed},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, settings.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("discord request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("discord returned status %d", resp.StatusCode)
	}

	return nil
}

func formatDiscordEmbed(p *Payload) (title, description string, color int) {
	switch p.EventType {
	case "incident.created":
		color = 0xE74C3C
		title = "Incident Opened"
		if p.Incident != nil {
			description = fmt.Sprintf("**%s** is down", p.Incident.MonitorName)
		}
	case "incident.resolved":
		color = 0x2ECC71
		title = "Incident Resolved"
		if p.Incident != nil {
			description = fmt.Sprintf("**%s** is back up", p.Incident.MonitorName)
		}
	case "incident.acknowledged":
		color = 0xF39C12
		title = "Incident Acknowledged"
		if p.Incident != nil {
			description = fmt.Sprintf("**%s** acknowledged by %s", p.Incident.MonitorName, p.Incident.AcknowledgedBy)
		}
	case "incident.reminder":
		color = 0xE74C3C
		title = "Incident Reminder"
		if p.Incident != nil {
			description = fmt.Sprintf("**%s** is still down", p.Incident.MonitorName)
		}
	case "incident.escalated":
		color = 0xE67E22
		title = fmt.Sprintf("Escalation Step %d/%d", p.EscalationStep, p.EscalationTotal)
		if p.Incident != nil {
			description = fmt.Sprintf("**%s**: %s", p.Incident.MonitorName, p.Incident.Cause)
		}
	case "content.changed":
		color = 0x3498DB
		title = "Content Changed"
		if p.Monitor != nil {
			description = fmt.Sprintf("Response body changed for **%s**", p.Monitor.Name)
		}
	case "cert.changed":
		color = 0xF39C12
		title = "Certificate Changed"
		if p.Monitor != nil {
			description = fmt.Sprintf("TLS certificate fingerprint changed for **%s**", p.Monitor.Name)
		}
	case "sla.breach":
		color = 0xE74C3C
		title = "SLA Breach"
		if p.Monitor != nil {
			description = fmt.Sprintf("SLA target at risk for **%s**", p.Monitor.Name)
		}
	case "test":
		color = 0x9B59B6
		title = "Test Notification"
		description = "This is a test notification from Asura"
	default:
		color = 0x95A5A6
		title = p.EventType
		description = FormatMessage(p)
	}
	return
}

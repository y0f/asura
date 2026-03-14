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

type OpsgenieSettings struct {
	APIKey string `json:"api_key"`
	Region string `json:"region,omitempty"`
}

type OpsgenieSender struct{}

func (s *OpsgenieSender) Type() string { return "opsgenie" }

func (s *OpsgenieSender) Send(ctx context.Context, channel *storage.NotificationChannel, payload *Payload) error {
	var settings OpsgenieSettings
	if err := json.Unmarshal(channel.Settings, &settings); err != nil {
		return fmt.Errorf("invalid opsgenie settings: %w", err)
	}
	if settings.APIKey == "" {
		return fmt.Errorf("opsgenie api_key is required")
	}

	baseURL := "https://api.opsgenie.com"
	if settings.Region == "eu" {
		baseURL = "https://api.eu.opsgenie.com"
	}

	alias := opsgenieAlias(payload)

	switch payload.EventType {
	case "incident.resolved":
		return opsgenieClose(ctx, baseURL, settings.APIKey, alias)
	case "incident.acknowledged":
		return opsgenieAck(ctx, baseURL, settings.APIKey, alias)
	default:
		return opsgenieCreate(ctx, baseURL, settings.APIKey, alias, payload)
	}
}

func opsgenieCreate(ctx context.Context, baseURL, apiKey, alias string, payload *Payload) error {
	priority := "P1"
	if payload.EventType == "incident.reminder" || payload.EventType == "content.changed" {
		priority = "P3"
	}

	alert := map[string]any{
		"message":     FormatMessage(payload),
		"alias":       alias,
		"description": FormatMessage(payload),
		"priority":    priority,
		"source":      "asura",
	}

	return opsgeniePost(ctx, baseURL+"/v2/alerts", apiKey, alert)
}

func opsgenieAck(ctx context.Context, baseURL, apiKey, alias string) error {
	url := fmt.Sprintf("%s/v2/alerts/%s/acknowledge?identifierType=alias", baseURL, alias)
	return opsgeniePost(ctx, url, apiKey, map[string]any{"source": "asura"})
}

func opsgenieClose(ctx context.Context, baseURL, apiKey, alias string) error {
	url := fmt.Sprintf("%s/v2/alerts/%s/close?identifierType=alias", baseURL, alias)
	return opsgeniePost(ctx, url, apiKey, map[string]any{"source": "asura"})
}

func opsgeniePost(ctx context.Context, url, apiKey string, data map[string]any) error {
	body, _ := json.Marshal(data)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "GenieKey "+apiKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("opsgenie request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("opsgenie returned status %d", resp.StatusCode)
	}
	return nil
}

func opsgenieAlias(p *Payload) string {
	if p.Incident != nil {
		return fmt.Sprintf("asura-monitor-%d-incident-%d", p.Incident.MonitorID, p.Incident.ID)
	}
	if p.Monitor != nil {
		return fmt.Sprintf("asura-monitor-%d", p.Monitor.ID)
	}
	return "asura-test"
}

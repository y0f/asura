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

type PagerDutySettings struct {
	RoutingKey string `json:"routing_key"`
}

type PagerDutySender struct{}

func (s *PagerDutySender) Type() string { return "pagerduty" }

func (s *PagerDutySender) Send(ctx context.Context, channel *storage.NotificationChannel, payload *Payload) error {
	var settings PagerDutySettings
	if err := json.Unmarshal(channel.Settings, &settings); err != nil {
		return fmt.Errorf("invalid pagerduty settings: %w", err)
	}
	if settings.RoutingKey == "" {
		return fmt.Errorf("pagerduty routing_key is required")
	}

	action, severity := pagerdutyAction(payload.EventType)
	dedupKey := pagerdutyDedupKey(payload)

	event := map[string]any{
		"routing_key":  settings.RoutingKey,
		"event_action": action,
		"dedup_key":    dedupKey,
	}

	if action == "trigger" || action == "acknowledge" {
		event["payload"] = map[string]any{
			"summary":   FormatMessage(payload),
			"source":    "asura",
			"severity":  severity,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}
	}

	body, _ := json.Marshal(event)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://events.pagerduty.com/v2/enqueue", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := newHTTPClient(false)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("pagerduty request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("pagerduty returned status %d", resp.StatusCode)
	}
	return nil
}

func pagerdutyAction(eventType string) (string, string) {
	switch eventType {
	case "incident.created", "incident.reminder", "content.changed", "test":
		return "trigger", "critical"
	case "incident.acknowledged":
		return "acknowledge", ""
	case "incident.resolved":
		return "resolve", ""
	default:
		return "trigger", "warning"
	}
}

func pagerdutyDedupKey(p *Payload) string {
	if p.Incident != nil {
		return fmt.Sprintf("asura-monitor-%d-incident-%d", p.Incident.MonitorID, p.Incident.ID)
	}
	if p.Monitor != nil {
		return fmt.Sprintf("asura-monitor-%d", p.Monitor.ID)
	}
	return "asura-test"
}

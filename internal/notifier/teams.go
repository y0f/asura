package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/y0f/asura/internal/storage"
)

type TeamsSettings struct {
	WebhookURL string `json:"webhook_url"`
}

type TeamsSender struct {
	AllowPrivate bool
}

func (s *TeamsSender) Type() string { return "teams" }

func (s *TeamsSender) Send(ctx context.Context, channel *storage.NotificationChannel, payload *Payload) error {
	var settings TeamsSettings
	if err := json.Unmarshal(channel.Settings, &settings); err != nil {
		return fmt.Errorf("invalid teams settings: %w", err)
	}
	if settings.WebhookURL == "" {
		return fmt.Errorf("teams webhook_url is required")
	}

	message := FormatMessage(payload)
	color := "Good"
	switch payload.EventType {
	case "incident.created", "incident.reminder":
		color = "Attention"
	case "incident.acknowledged":
		color = "Warning"
	}

	card := map[string]any{
		"type": "message",
		"attachments": []map[string]any{
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content": map[string]any{
					"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
					"type":    "AdaptiveCard",
					"version": "1.4",
					"body": []map[string]any{
						{
							"type":   "TextBlock",
							"text":   message,
							"weight": "Bolder",
							"size":   "Medium",
							"color":  color,
						},
						{
							"type":      "TextBlock",
							"text":      fmt.Sprintf("Event: %s", payload.EventType),
							"isSubtle":  true,
							"spacing":   "Small",
							"separator": true,
						},
					},
				},
			},
		},
	}

	body, _ := json.Marshal(card)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, settings.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := newHTTPClient(s.AllowPrivate).Do(req)
	if err != nil {
		return fmt.Errorf("teams request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("teams returned status %d", resp.StatusCode)
	}
	return nil
}

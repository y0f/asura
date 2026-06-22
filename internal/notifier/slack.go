package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/y0f/asura/internal/storage"
)

type SlackSettings struct {
	WebhookURL string `json:"webhook_url"`
	Channel    string `json:"channel,omitempty"`
}

type SlackSender struct {
	AllowPrivate bool
}

func (s *SlackSender) Type() string { return "slack" }

func (s *SlackSender) Send(ctx context.Context, channel *storage.NotificationChannel, payload *Payload) error {
	var settings SlackSettings
	if err := json.Unmarshal(channel.Settings, &settings); err != nil {
		return fmt.Errorf("invalid slack settings: %w", err)
	}

	if settings.WebhookURL == "" {
		return fmt.Errorf("slack webhook_url is required")
	}

	text := escapeSlackMrkdwn(FormatMessage(payload))

	msg := map[string]any{
		"text": text,
		"blocks": []map[string]any{
			{
				"type": "section",
				"text": map[string]string{
					"type": "mrkdwn",
					"text": text,
				},
			},
		},
	}
	if settings.Channel != "" {
		msg["channel"] = settings.Channel
	}

	body, _ := json.Marshal(msg)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, settings.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := newHTTPClient(s.AllowPrivate).Do(req)
	if err != nil {
		return fmt.Errorf("slack request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("slack returned status %d", resp.StatusCode)
	}

	return nil
}

// escapeSlackMrkdwn escapes Slack mrkdwn special characters and link patterns
// to prevent @everyone/@channel pings and formatting exploits.
func escapeSlackMrkdwn(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

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

type GoogleChatSettings struct {
	WebhookURL string `json:"webhook_url"`
}

type GoogleChatSender struct {
	AllowPrivate bool
}

func (s *GoogleChatSender) Type() string { return "googlechat" }

func (s *GoogleChatSender) Send(ctx context.Context, channel *storage.NotificationChannel, payload *Payload) error {
	var settings GoogleChatSettings
	if err := json.Unmarshal(channel.Settings, &settings); err != nil {
		return fmt.Errorf("invalid googlechat settings: %w", err)
	}
	if settings.WebhookURL == "" {
		return fmt.Errorf("googlechat webhook_url is required")
	}

	text := FormatMessage(payload)
	msg := map[string]any{
		"text": text,
	}

	body, _ := json.Marshal(msg)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, settings.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := newHTTPClient(s.AllowPrivate).Do(req)
	if err != nil {
		return fmt.Errorf("googlechat request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("googlechat returned status %d", resp.StatusCode)
	}
	return nil
}

package notifier

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/y0f/asura/internal/storage"
)

// WebhookSettings holds webhook-specific configuration.
type WebhookSettings struct {
	URL    string `json:"url"`
	Secret string `json:"secret,omitempty"` // HMAC-SHA256 signing secret
}

type WebhookSender struct {
	AllowPrivate bool
}

func (s *WebhookSender) Type() string { return "webhook" }

func (s *WebhookSender) Send(ctx context.Context, channel *storage.NotificationChannel, payload *Payload) error {
	var settings WebhookSettings
	if err := json.Unmarshal(channel.Settings, &settings); err != nil {
		return fmt.Errorf("invalid webhook settings: %w", err)
	}
	if settings.URL == "" {
		return fmt.Errorf("webhook URL is required")
	}

	body, err := marshalPayload(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, settings.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Asura/1.0")

	// HMAC-SHA256 signature
	if settings.Secret != "" {
		mac := hmac.New(sha256.New, []byte(settings.Secret))
		mac.Write(body)
		sig := hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-Asura-Signature", "sha256="+sig)
	}

	resp, err := newHTTPClient(s.AllowPrivate).Do(req)
	if err != nil {
		return fmt.Errorf("webhook request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	return nil
}

package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/y0f/asura/internal/storage"
)

type NtfySettings struct {
	ServerURL string `json:"server_url"`
	Topic     string `json:"topic"`
	Priority  int    `json:"priority,omitempty"`
	Tags      string `json:"tags,omitempty"`
	ClickURL  string `json:"click_url,omitempty"`
}

type NtfySender struct {
	AllowPrivate bool
}

func (s *NtfySender) Type() string { return "ntfy" }

func (s *NtfySender) Send(ctx context.Context, channel *storage.NotificationChannel, payload *Payload) error {
	var settings NtfySettings
	if err := json.Unmarshal(channel.Settings, &settings); err != nil {
		return fmt.Errorf("invalid ntfy settings: %w", err)
	}

	if settings.Topic == "" {
		return fmt.Errorf("ntfy topic is required")
	}

	serverURL := settings.ServerURL
	if serverURL == "" {
		serverURL = "https://ntfy.sh"
	}
	serverURL = strings.TrimRight(serverURL, "/")

	message := FormatMessage(payload)
	url := fmt.Sprintf("%s/%s", serverURL, settings.Topic)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(message))
	if err != nil {
		return err
	}

	req.Header.Set("Title", fmt.Sprintf("Asura — %s", payload.EventType))
	if settings.Priority > 0 && settings.Priority <= 5 {
		req.Header.Set("Priority", fmt.Sprintf("%d", settings.Priority))
	}
	if settings.Tags != "" {
		req.Header.Set("Tags", settings.Tags)
	}
	if settings.ClickURL != "" {
		req.Header.Set("Click", settings.ClickURL)
	}

	resp, err := newHTTPClient(s.AllowPrivate).Do(req)
	if err != nil {
		return fmt.Errorf("ntfy request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("ntfy returned status %d", resp.StatusCode)
	}

	return nil
}

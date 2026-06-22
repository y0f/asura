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

type GotifySettings struct {
	ServerURL string `json:"server_url"`
	AppToken  string `json:"app_token"`
	Priority  int    `json:"priority,omitempty"`
}

type GotifySender struct {
	AllowPrivate bool
}

func (s *GotifySender) Type() string { return "gotify" }

func (s *GotifySender) Send(ctx context.Context, channel *storage.NotificationChannel, payload *Payload) error {
	var settings GotifySettings
	if err := json.Unmarshal(channel.Settings, &settings); err != nil {
		return fmt.Errorf("invalid gotify settings: %w", err)
	}
	if settings.ServerURL == "" {
		return fmt.Errorf("gotify server_url is required")
	}
	if settings.AppToken == "" {
		return fmt.Errorf("gotify app_token is required")
	}

	priority := settings.Priority
	if priority == 0 {
		priority = 5
	}

	text := FormatMessage(payload)
	title := "Asura Alert"
	if payload.Monitor != nil {
		title = payload.Monitor.Name
	}

	body, _ := json.Marshal(map[string]any{
		"title":    title,
		"message":  text,
		"priority": priority,
	})

	serverURL := strings.TrimRight(settings.ServerURL, "/")
	endpoint := serverURL + "/message"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", settings.AppToken)

	resp, err := newHTTPClient(s.AllowPrivate).Do(req)
	if err != nil {
		return fmt.Errorf("gotify request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("gotify returned status %d", resp.StatusCode)
	}
	return nil
}

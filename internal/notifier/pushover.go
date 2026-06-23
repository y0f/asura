package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/y0f/asura/internal/storage"
)

type PushoverSettings struct {
	UserKey  string `json:"user_key"`
	AppToken string `json:"app_token"`
	Priority int    `json:"priority,omitempty"`
	Sound    string `json:"sound,omitempty"`
	Device   string `json:"device,omitempty"`
}

type PushoverSender struct{}

func (s *PushoverSender) Type() string { return "pushover" }

func (s *PushoverSender) Send(ctx context.Context, channel *storage.NotificationChannel, payload *Payload) error {
	var settings PushoverSettings
	if err := json.Unmarshal(channel.Settings, &settings); err != nil {
		return fmt.Errorf("invalid pushover settings: %w", err)
	}
	if settings.UserKey == "" {
		return fmt.Errorf("pushover user_key is required")
	}
	if settings.AppToken == "" {
		return fmt.Errorf("pushover app_token is required")
	}

	form := pushoverForm(&settings, payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.pushover.net/1/messages.json",
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := newHTTPClient(false)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("pushover request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("pushover returned status %d", resp.StatusCode)
	}
	return nil
}

func pushoverForm(settings *PushoverSettings, payload *Payload) url.Values {
	priority := pushoverPriority(settings.Priority, payload.EventType)
	form := url.Values{
		"token":   {settings.AppToken},
		"user":    {settings.UserKey},
		"message": {FormatMessage(payload)},
		"title":   {"Asura"},
	}
	if priority != 0 {
		form.Set("priority", strconv.Itoa(priority))
	}
	if priority == 2 {
		form.Set("retry", "300")
		form.Set("expire", "3600")
	}
	if settings.Sound != "" {
		form.Set("sound", settings.Sound)
	}
	if settings.Device != "" {
		form.Set("device", settings.Device)
	}
	return form
}

func pushoverPriority(configured int, eventType string) int {
	if configured != 0 {
		return configured
	}
	switch eventType {
	case "incident.created":
		return 1
	case "incident.reminder":
		return 0
	case "incident.resolved":
		return -1
	default:
		return 0
	}
}

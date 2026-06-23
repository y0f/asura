package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"

	"github.com/y0f/asura/internal/storage"
)

type TelegramSettings struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

type TelegramSender struct{}

func (s *TelegramSender) Type() string { return "telegram" }

func (s *TelegramSender) Send(ctx context.Context, channel *storage.NotificationChannel, payload *Payload) error {
	var settings TelegramSettings
	if err := json.Unmarshal(channel.Settings, &settings); err != nil {
		return fmt.Errorf("invalid telegram settings: %w", err)
	}

	if settings.BotToken == "" || settings.ChatID == "" {
		return fmt.Errorf("telegram bot_token and chat_id are required")
	}

	text := html.EscapeString(FormatMessage(payload))
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", settings.BotToken)

	body, _ := json.Marshal(map[string]any{
		"chat_id":    settings.ChatID,
		"text":       text,
		"parse_mode": "HTML",
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := newHTTPClient(false)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("telegram returned status %d", resp.StatusCode)
	}

	return nil
}

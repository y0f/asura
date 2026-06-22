package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/y0f/asura/internal/storage"
)

type MatrixSettings struct {
	Homeserver  string `json:"homeserver"`
	AccessToken string `json:"access_token"`
	RoomID      string `json:"room_id"`
}

type MatrixSender struct {
	AllowPrivate bool
}

func (s *MatrixSender) Type() string { return "matrix" }

func (s *MatrixSender) Send(ctx context.Context, channel *storage.NotificationChannel, payload *Payload) error {
	var settings MatrixSettings
	if err := json.Unmarshal(channel.Settings, &settings); err != nil {
		return fmt.Errorf("invalid matrix settings: %w", err)
	}
	if settings.Homeserver == "" {
		return fmt.Errorf("matrix homeserver is required")
	}
	if settings.AccessToken == "" {
		return fmt.Errorf("matrix access_token is required")
	}
	if settings.RoomID == "" {
		return fmt.Errorf("matrix room_id is required")
	}

	homeserver := strings.TrimRight(settings.Homeserver, "/")
	txnID := fmt.Sprintf("%d%d", time.Now().UnixNano(), rand.Int63n(100000))
	roomIDEncoded := url.PathEscape(settings.RoomID)
	endpoint := fmt.Sprintf("%s/_matrix/client/r0/rooms/%s/send/m.room.message/%s", homeserver, roomIDEncoded, txnID)

	text := FormatMessage(payload)
	body, _ := json.Marshal(map[string]string{
		"msgtype": "m.text",
		"body":    text,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+settings.AccessToken)

	resp, err := newHTTPClient(s.AllowPrivate).Do(req)
	if err != nil {
		return fmt.Errorf("matrix request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("matrix returned status %d", resp.StatusCode)
	}
	return nil
}

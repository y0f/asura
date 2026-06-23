package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/y0f/asura/internal/safenet"
	"github.com/y0f/asura/internal/storage"
)

type WebSocketChecker struct {
	AllowPrivate bool
}

func (c *WebSocketChecker) Type() string { return "websocket" }

func (c *WebSocketChecker) Check(ctx context.Context, monitor *storage.Monitor) (*Result, error) {
	var settings storage.WebSocketSettings
	if len(monitor.Settings) > 0 {
		if err := json.Unmarshal(monitor.Settings, &settings); err != nil {
			return &Result{Status: "down", Message: fmt.Sprintf("invalid settings: %v", err)}, nil
		}
	}

	timeout := time.Duration(monitor.Timeout) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	baseDial := (&net.Dialer{
		Timeout: timeout,
		Control: safenet.MaybeDialControl(c.AllowPrivate),
	}).DialContext

	transport := &http.Transport{DialContext: baseDial}
	if monitor.ProxyURL != "" {
		if socks := ProxyDialer(monitor.ProxyURL, baseDial, c.AllowPrivate); socks != nil {
			transport.DialContext = socks
		} else if pu := HTTPProxyURL(monitor.ProxyURL); pu != nil {
			if u, perr := url.Parse(monitor.Target); perr == nil {
				if err := validateProxyTarget(ctx, u.Host, c.AllowPrivate); err != nil {
					return &Result{Status: "down", Message: err.Error()}, nil
				}
			}
			transport.Proxy = http.ProxyURL(pu)
		}
	}

	opts := &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: transport},
	}
	if len(settings.Headers) > 0 {
		header := http.Header{}
		for k, v := range settings.Headers {
			header.Set(k, v)
		}
		opts.HTTPHeader = header
	}

	start := time.Now()
	conn, _, err := websocket.Dial(ctx, monitor.Target, opts)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		return &Result{
			Status:       "down",
			ResponseTime: elapsed,
			Message:      fmt.Sprintf("WebSocket dial failed: %v", err),
		}, nil
	}
	defer conn.CloseNow()

	if settings.SendMessage != "" {
		err := conn.Write(ctx, websocket.MessageText, []byte(settings.SendMessage))
		if err != nil {
			return &Result{
				Status:       "down",
				ResponseTime: elapsed,
				Message:      fmt.Sprintf("WebSocket write failed: %v", err),
			}, nil
		}
	}

	if settings.ExpectReply != "" {
		_, msg, err := conn.Read(ctx)
		if err != nil {
			return &Result{
				Status:       "down",
				ResponseTime: elapsed,
				Message:      fmt.Sprintf("WebSocket read failed: %v", err),
			}, nil
		}
		if !strings.Contains(string(msg), settings.ExpectReply) {
			return &Result{
				Status:       "down",
				ResponseTime: elapsed,
				Message:      "expected reply not found in WebSocket message",
			}, nil
		}
	}

	conn.Close(websocket.StatusNormalClosure, "check complete")

	return &Result{
		Status:       "up",
		ResponseTime: time.Since(start).Milliseconds(),
		Message:      "WebSocket connection successful",
	}, nil
}

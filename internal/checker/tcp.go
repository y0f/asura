package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/y0f/asura/internal/safenet"
	"github.com/y0f/asura/internal/storage"
)

type TCPChecker struct {
	AllowPrivate bool
}

func (c *TCPChecker) Type() string { return "tcp" }

func (c *TCPChecker) Check(ctx context.Context, monitor *storage.Monitor) (*Result, error) {
	var settings storage.TCPSettings
	if len(monitor.Settings) > 0 {
		if err := json.Unmarshal(monitor.Settings, &settings); err != nil {
			return &Result{Status: "down", Message: fmt.Sprintf("invalid settings: %v", err)}, nil
		}
	}

	timeout := time.Duration(monitor.Timeout) * time.Second
	baseDial := (&net.Dialer{Timeout: timeout, Control: safenet.MaybeDialControl(c.AllowPrivate)}).DialContext

	dialFn := baseDial
	if socks := ProxyDialer(monitor.ProxyURL, baseDial, c.AllowPrivate); socks != nil {
		dialFn = socks
	}

	start := time.Now()
	conn, err := dialFn(ctx, "tcp", monitor.Target)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		return &Result{
			Status:       "down",
			ResponseTime: elapsed,
			Message:      fmt.Sprintf("connection failed: %v", err),
		}, nil
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(timeout))

	if settings.SendData != "" {
		_, err := conn.Write([]byte(settings.SendData))
		if err != nil {
			return &Result{
				Status:       "down",
				ResponseTime: elapsed,
				Message:      fmt.Sprintf("send failed: %v", err),
			}, nil
		}
	}

	if settings.ExpectData != "" {
		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		if err != nil {
			return &Result{
				Status:       "down",
				ResponseTime: elapsed,
				Message:      fmt.Sprintf("read failed: %v", err),
			}, nil
		}
		received := string(buf[:n])
		if !strings.Contains(received, settings.ExpectData) {
			return &Result{
				Status:       "down",
				ResponseTime: elapsed,
				Message:      "expected data not found in response",
			}, nil
		}
	}

	return &Result{
		Status:       "up",
		ResponseTime: elapsed,
		Message:      "connection successful",
	}, nil
}

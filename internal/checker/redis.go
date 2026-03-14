package checker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/y0f/asura/internal/safenet"
	"github.com/y0f/asura/internal/storage"
)

type RedisChecker struct {
	AllowPrivate bool
}

func (c *RedisChecker) Type() string { return "redis" }

func (c *RedisChecker) Check(ctx context.Context, monitor *storage.Monitor) (*Result, error) {
	var settings storage.RedisSettings
	if len(monitor.Settings) > 0 {
		if err := json.Unmarshal(monitor.Settings, &settings); err != nil {
			return &Result{Status: "down", Message: fmt.Sprintf("invalid settings: %v", err)}, nil
		}
	}

	target := monitor.Target
	if !strings.Contains(target, ":") {
		target += ":6379"
	}

	timeout := time.Duration(monitor.Timeout) * time.Second
	dialer := &net.Dialer{Timeout: timeout, Control: safenet.MaybeDialControl(c.AllowPrivate)}

	start := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp", target)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("connection failed: %v", err)}, nil
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))

	reader := bufio.NewReader(conn)

	if settings.Password != "" {
		fmt.Fprintf(conn, "*2\r\n$4\r\nAUTH\r\n$%d\r\n%s\r\n", len(settings.Password), settings.Password)
		line, err := reader.ReadString('\n')
		if err != nil || !strings.HasPrefix(line, "+OK") {
			return &Result{Status: "down", ResponseTime: elapsed, Message: "AUTH failed"}, nil
		}
	}

	fmt.Fprintf(conn, "*1\r\n$4\r\nPING\r\n")
	line, err := reader.ReadString('\n')
	elapsed = time.Since(start).Milliseconds()
	if err != nil {
		return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("PING failed: %v", err)}, nil
	}
	if !strings.HasPrefix(line, "+PONG") {
		return &Result{Status: "down", ResponseTime: elapsed, Message: "unexpected response: " + strings.TrimSpace(line)}, nil
	}

	return &Result{Status: "up", ResponseTime: elapsed, Message: "PONG"}, nil
}

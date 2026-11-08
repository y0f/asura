package checker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/y0f/asura/v2/internal/model"
	"github.com/y0f/asura/v2/internal/safenet"
)

type RedisChecker struct {
	AllowPrivate bool
}

func (c *RedisChecker) Type() string { return "redis" }

const maxRedisReplyBytes = 4 << 10

func readRedisReply(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) || len(line) > maxRedisReplyBytes {
		return "", errors.New("Redis reply exceeds 4 KiB")
	}
	if err != nil {
		return "", err
	}
	if len(line) < 3 || line[len(line)-2] != '\r' {
		return "", errors.New("invalid Redis reply terminator")
	}
	return string(line[:len(line)-2]), nil
}

func (c *RedisChecker) Check(ctx context.Context, monitor *model.Monitor) (*Result, error) {
	var settings model.RedisSettings
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
	defer closeConnOnCancel(ctx, conn)()
	conn.SetDeadline(time.Now().Add(timeout))

	reader := bufio.NewReaderSize(conn, maxRedisReplyBytes)

	if settings.Password != "" {
		if _, err := fmt.Fprintf(conn, "*2\r\n$4\r\nAUTH\r\n$%d\r\n%s\r\n", len(settings.Password), settings.Password); err != nil {
			return &Result{Status: "down", ResponseTime: time.Since(start).Milliseconds(), Message: "AUTH write failed"}, nil
		}
		line, err := readRedisReply(reader)
		if err != nil || line != "+OK" {
			return &Result{Status: "down", ResponseTime: time.Since(start).Milliseconds(), Message: "AUTH failed"}, nil
		}
	}

	if _, err := fmt.Fprintf(conn, "*1\r\n$4\r\nPING\r\n"); err != nil {
		return &Result{Status: "down", ResponseTime: time.Since(start).Milliseconds(), Message: "PING write failed"}, nil
	}
	line, err := readRedisReply(reader)
	elapsed = time.Since(start).Milliseconds()
	if err != nil {
		return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("PING failed: %v", err)}, nil
	}
	if line != "+PONG" {
		return &Result{Status: "down", ResponseTime: elapsed, Message: "unexpected response: " + strings.TrimSpace(line)}, nil
	}

	return &Result{Status: "up", ResponseTime: elapsed, Message: "PONG"}, nil
}

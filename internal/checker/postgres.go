package checker

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/y0f/asura/internal/safenet"
	"github.com/y0f/asura/internal/storage"
)

type PostgreSQLChecker struct {
	AllowPrivate bool
}

func (c *PostgreSQLChecker) Type() string { return "postgresql" }

func (c *PostgreSQLChecker) Check(ctx context.Context, monitor *storage.Monitor) (*Result, error) {
	var settings storage.PostgreSQLSettings
	if len(monitor.Settings) > 0 {
		if err := json.Unmarshal(monitor.Settings, &settings); err != nil {
			return &Result{Status: "down", Message: fmt.Sprintf("invalid settings: %v", err)}, nil
		}
	}

	target := monitor.Target
	if !strings.Contains(target, ":") {
		target += ":5432"
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

	user := settings.Username
	if user == "" {
		user = "postgres"
	}
	db := settings.Database
	if db == "" {
		db = "postgres"
	}

	startup := buildStartupMessage(user, db)
	if _, err := conn.Write(startup); err != nil {
		return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("startup failed: %v", err)}, nil
	}

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	elapsed = time.Since(start).Milliseconds()
	if err != nil {
		return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("read failed: %v", err)}, nil
	}

	if n > 0 && buf[0] == 'R' {
		return &Result{Status: "up", ResponseTime: elapsed, Message: "PostgreSQL accepting connections"}, nil
	}
	if n > 0 && buf[0] == 'E' {
		msg := extractPGError(buf[:n])
		return &Result{Status: "up", ResponseTime: elapsed, Message: "PostgreSQL up: " + msg}, nil
	}

	return &Result{Status: "up", ResponseTime: elapsed, Message: "PostgreSQL responded"}, nil
}

func buildStartupMessage(user, database string) []byte {
	params := map[string]string{
		"user":     user,
		"database": database,
	}

	var payload []byte
	payload = binary.BigEndian.AppendUint32(payload, 196608) // protocol 3.0
	for k, v := range params {
		payload = append(payload, []byte(k)...)
		payload = append(payload, 0)
		payload = append(payload, []byte(v)...)
		payload = append(payload, 0)
	}
	payload = append(payload, 0) // terminator

	msg := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(msg, uint32(4+len(payload)))
	copy(msg[4:], payload)
	return msg
}

func extractPGError(buf []byte) string {
	for i := 1; i < len(buf); i++ {
		if buf[i] == 'M' && i+1 < len(buf) {
			end := i + 1
			for end < len(buf) && buf[end] != 0 {
				end++
			}
			return string(buf[i+1 : end])
		}
	}
	return "error response"
}

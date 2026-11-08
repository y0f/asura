package checker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/y0f/asura/v2/internal/model"
	"github.com/y0f/asura/v2/internal/safenet"
)

type PostgreSQLChecker struct {
	AllowPrivate bool
}

func (c *PostgreSQLChecker) Type() string { return "postgresql" }

func (c *PostgreSQLChecker) Check(ctx context.Context, monitor *model.Monitor) (*Result, error) {
	var settings model.PostgreSQLSettings
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
	defer closeConnOnCancel(ctx, conn)()
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

	var header [5]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return &Result{Status: "down", ResponseTime: time.Since(start).Milliseconds(), Message: fmt.Sprintf("read failed: %v", err)}, nil
	}
	// The declared length includes its four bytes, but excludes the type byte.
	length := binary.BigEndian.Uint32(header[1:])
	if length < 4 || length > 64*1024 {
		return &Result{Status: "down", ResponseTime: time.Since(start).Milliseconds(), Message: "invalid PostgreSQL response length"}, nil
	}
	payload := make([]byte, int(length)-4)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return &Result{Status: "down", ResponseTime: time.Since(start).Milliseconds(), Message: fmt.Sprintf("read failed: %v", err)}, nil
	}
	elapsed = time.Since(start).Milliseconds()

	if header[0] == 'R' && validPGAuthentication(payload) {
		return &Result{Status: "up", ResponseTime: elapsed, Message: "PostgreSQL accepting connections"}, nil
	}
	if header[0] == 'E' {
		if msg, ok := parsePGError(payload); ok {
			return &Result{Status: "up", ResponseTime: elapsed, Message: "PostgreSQL up: " + msg}, nil
		}
	}

	return &Result{Status: "down", ResponseTime: elapsed, Message: "unexpected PostgreSQL response"}, nil
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

func validPGAuthentication(payload []byte) bool {
	if len(payload) < 4 {
		return false
	}
	switch binary.BigEndian.Uint32(payload[:4]) {
	case 0, 2, 3, 7, 9: // OK, Kerberos V5, cleartext, GSS, SSPI.
		return len(payload) == 4
	case 5: // MD5 includes a four-byte salt.
		return len(payload) == 8
	case 8, 11, 12: // GSS/SASL continuation and SASL final carry opaque bytes.
		return true
	case 10: // SASL mechanism names followed by an extra zero terminator.
		mechanisms := payload[4:]
		for len(mechanisms) > 0 {
			end := bytes.IndexByte(mechanisms, 0)
			if end <= 0 {
				return false
			}
			mechanisms = mechanisms[end+1:]
			if len(mechanisms) == 1 && mechanisms[0] == 0 {
				return true
			}
		}
	}
	return false
}

func parsePGError(payload []byte) (string, bool) {
	var seen [256]bool
	var message string
	for len(payload) > 0 {
		kind := payload[0]
		payload = payload[1:]
		if kind == 0 {
			return message, len(payload) == 0 && seen['S'] && seen['C'] && seen['M']
		}
		end := bytes.IndexByte(payload, 0)
		if end < 0 || seen[kind] || (end == 0 && (kind == 'S' || kind == 'C' || kind == 'M')) {
			return "", false
		}
		seen[kind] = true
		if kind == 'C' {
			if end != 5 {
				return "", false
			}
			for _, b := range payload[:end] {
				if (b < '0' || b > '9') && (b < 'A' || b > 'Z') {
					return "", false
				}
			}
		}
		if kind == 'M' {
			message = string(payload[:end])
		}
		payload = payload[end+1:]
	}
	return "", false
}

package checker

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/y0f/asura/internal/safenet"
	"github.com/y0f/asura/internal/storage"
)

type SMTPChecker struct {
	AllowPrivate bool
}

func (c *SMTPChecker) Type() string { return "smtp" }

func (c *SMTPChecker) Check(ctx context.Context, monitor *storage.Monitor) (*Result, error) {
	var settings storage.SMTPSettings
	if len(monitor.Settings) > 0 {
		if err := json.Unmarshal(monitor.Settings, &settings); err != nil {
			return &Result{Status: "down", Message: fmt.Sprintf("invalid settings: %v", err)}, nil
		}
	}

	port := settings.Port
	if port == 0 {
		port = 25
	}

	target := monitor.Target
	if !strings.Contains(target, ":") {
		target = fmt.Sprintf("%s:%d", target, port)
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

	host, _, _ := net.SplitHostPort(target)

	if settings.ExpectBanner != "" {
		buf := make([]byte, 512)
		n, rerr := conn.Read(buf)
		if rerr != nil {
			return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("failed to read banner: %v", rerr)}, nil
		}
		if !strings.Contains(string(buf[:n]), settings.ExpectBanner) {
			return &Result{Status: "down", ResponseTime: elapsed, Message: "banner mismatch"}, nil
		}
		// Replay the consumed greeting so smtp.NewClient still reads the 220 line.
		conn = &prefixedConn{Conn: conn, r: io.MultiReader(bytes.NewReader(buf[:n]), conn)}
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("smtp handshake failed: %v", err)}, nil
	}
	defer client.Close()

	ehloHost := "asura"
	if err := client.Hello(ehloHost); err != nil {
		return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("EHLO failed: %v", err)}, nil
	}

	if settings.STARTTLS {
		ok, _ := client.Extension("STARTTLS")
		if ok {
			if err := client.StartTLS(&tls.Config{ServerName: host, InsecureSkipVerify: false}); err != nil {
				return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("STARTTLS failed: %v", err)}, nil
			}
		} else {
			return &Result{Status: "down", ResponseTime: elapsed, Message: "server does not support STARTTLS"}, nil
		}
	}

	elapsed = time.Since(start).Milliseconds()
	client.Quit()

	return &Result{Status: "up", ResponseTime: elapsed, Message: "SMTP OK"}, nil
}

// prefixedConn replays bytes already read from the connection to the next
// reader, so the SMTP greeting can be inspected for a banner match before
// smtp.NewClient consumes it.
type prefixedConn struct {
	net.Conn
	r io.Reader
}

func (c *prefixedConn) Read(b []byte) (int, error) { return c.r.Read(b) }

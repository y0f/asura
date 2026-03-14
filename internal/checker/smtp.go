package checker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
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

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("smtp handshake failed: %v", err)}, nil
	}
	defer client.Close()

	if settings.ExpectBanner != "" {
		buf := make([]byte, 1024)
		n, _ := conn.Read(buf)
		banner := string(buf[:n])
		if !strings.Contains(banner, settings.ExpectBanner) {
			return &Result{Status: "down", ResponseTime: elapsed, Message: "unexpected banner"}, nil
		}
	}

	ehloHost := "asura"
	if err := client.Hello(ehloHost); err != nil {
		return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("EHLO failed: %v", err)}, nil
	}

	if settings.STARTTLS {
		ok, _ := client.Extension("STARTTLS")
		if ok {
			if err := client.StartTLS(&tls.Config{ServerName: host, InsecureSkipVerify: true}); err != nil {
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

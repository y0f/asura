package checker

import (
	"bufio"
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

	"github.com/y0f/asura/internal/model"
	"github.com/y0f/asura/internal/safenet"
)

type SMTPChecker struct {
	AllowPrivate bool
}

func (c *SMTPChecker) Type() string { return "smtp" }

func (c *SMTPChecker) Check(ctx context.Context, monitor *model.Monitor) (*Result, error) {
	var settings model.SMTPSettings
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

	reader := bufio.NewReaderSize(conn, 512)
	greeting, err := readSMTPGreeting(reader)
	if err != nil {
		return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("failed to read banner: %v", err)}, nil
	}
	if settings.ExpectBanner != "" && !strings.Contains(string(greeting), settings.ExpectBanner) {
		return &Result{Status: "down", ResponseTime: elapsed, Message: "banner mismatch"}, nil
	}
	// net/smtp requires a space after a bare reply code.
	if len(greeting) == 5 || bytes.HasSuffix(greeting, []byte("\r\n220\r\n")) {
		greeting = append(greeting[:len(greeting)-2], ' ', '\r', '\n')
	}
	conn = &prefixedConn{Conn: conn, r: io.MultiReader(bytes.NewReader(greeting), reader)}

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

func readSMTPGreeting(reader *bufio.Reader) ([]byte, error) {
	var greeting []byte
	for {
		line, err := reader.ReadSlice('\n')
		if err != nil {
			return nil, err
		}
		if len(line) > 512 || len(greeting)+len(line) > 64*1024 {
			return nil, fmt.Errorf("SMTP greeting too large")
		}
		if len(line) < 5 || !bytes.HasSuffix(line, []byte("\r\n")) || string(line[:3]) != "220" {
			return nil, fmt.Errorf("invalid SMTP greeting")
		}
		greeting = append(greeting, line...)
		if len(line) == 5 || line[3] == ' ' {
			return greeting, nil
		}
		if line[3] != '-' {
			return nil, fmt.Errorf("invalid SMTP greeting separator")
		}
	}
}

// prefixedConn replays the inspected greeting and preserves buffered bytes.
type prefixedConn struct {
	net.Conn
	r io.Reader
}

func (c *prefixedConn) Read(b []byte) (int, error) { return c.r.Read(b) }

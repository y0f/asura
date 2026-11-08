package checker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/y0f/asura/v2/internal/model"
	"github.com/y0f/asura/v2/internal/safenet"
)

type SMTPChecker struct {
	AllowPrivate bool
}

func (c *SMTPChecker) Type() string { return "smtp" }

const maxSMTPResponseBytes = 1 << 20

var errSMTPResponseLimit = errors.New("SMTP response traffic exceeds 1 MiB")

// Bound real inbound traffic, including TLS records after STARTTLS. A limit
// below net/smtp also covers its internal EHLO/HELO and post-upgrade readers.
// Return an explicit error rather than EOF so a partial reply cannot succeed.
type smtpResponseConn struct {
	net.Conn
	remaining int
	exceeded  atomic.Bool
}

func (c *smtpResponseConn) Read(p []byte) (int, error) {
	if c.remaining == 0 {
		c.exceeded.Store(true)
		return 0, errSMTPResponseLimit
	}
	if len(p) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.Conn.Read(p)
	c.remaining -= n
	return n, err
}

func (c *smtpResponseConn) Write(p []byte) (int, error) {
	if err := c.check(nil); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}

// bufio.ReadLine can treat bytes before a read error as a complete final line.
// Check the latched limit after each SMTP operation, including QUIT, so a
// truncated "250 ..." or "221 ..." reply cannot turn exhaustion into success.
func (c *smtpResponseConn) check(err error) error {
	if c.exceeded.Load() {
		return errSMTPResponseLimit
	}
	return err
}

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
	defer closeConnOnCancel(ctx, conn)()
	conn.SetDeadline(time.Now().Add(timeout))
	response := &smtpResponseConn{Conn: conn, remaining: maxSMTPResponseBytes}
	conn = response

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
	err = response.check(err)
	if err != nil {
		return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("smtp handshake failed: %v", err)}, nil
	}
	defer client.Close()

	ehloHost := "asura"
	if err := response.check(client.Hello(ehloHost)); err != nil {
		return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("EHLO failed: %v", err)}, nil
	}

	if settings.STARTTLS {
		ok, _ := client.Extension("STARTTLS")
		if ok {
			if err := response.check(client.StartTLS(&tls.Config{ServerName: host, InsecureSkipVerify: false})); err != nil {
				return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("STARTTLS failed: %v", err)}, nil
			}
		} else {
			return &Result{Status: "down", ResponseTime: elapsed, Message: "server does not support STARTTLS"}, nil
		}
	}

	if err := response.check(client.Quit()); err != nil {
		return &Result{Status: "down", ResponseTime: time.Since(start).Milliseconds(), Message: fmt.Sprintf("QUIT failed: %v", err)}, nil
	}

	return &Result{Status: "up", ResponseTime: time.Since(start).Milliseconds(), Message: "SMTP OK"}, nil
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

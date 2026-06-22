package checker

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/y0f/asura/internal/safenet"
	"github.com/y0f/asura/internal/storage"
)

type SSHChecker struct {
	AllowPrivate bool
}

func (c *SSHChecker) Type() string { return "ssh" }

func (c *SSHChecker) Check(ctx context.Context, monitor *storage.Monitor) (*Result, error) {
	var settings storage.SSHSettings
	if len(monitor.Settings) > 0 {
		if err := json.Unmarshal(monitor.Settings, &settings); err != nil {
			return &Result{Status: "down", Message: fmt.Sprintf("invalid settings: %v", err)}, nil
		}
	}

	target := monitor.Target
	if !strings.Contains(target, ":") {
		target = target + ":22"
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

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return &Result{Status: "down", ResponseTime: elapsed, Message: "no SSH banner received"}, nil
	}
	banner := scanner.Text()

	if !strings.HasPrefix(banner, "SSH-") {
		return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("invalid SSH banner: %s", banner)}, nil
	}

	// NOTE: This verifies the SSH identification banner, not the host key.
	// The banner carries no cryptographic identity and offers no MITM
	// protection. Real host-key verification requires an SSH transport
	// handshake (golang.org/x/crypto/ssh HostKeyCallback), which is not a
	// dependency of this module.
	if settings.ExpectedFingerprint != "" {
		h := sha256.Sum256([]byte(banner))
		fp := hex.EncodeToString(h[:])
		if !strings.EqualFold(fp, settings.ExpectedFingerprint) {
			return &Result{Status: "down", ResponseTime: elapsed, Message: "SSH banner fingerprint mismatch"}, nil
		}
	}

	elapsed = time.Since(start).Milliseconds()
	return &Result{Status: "up", ResponseTime: elapsed, Message: banner}, nil
}

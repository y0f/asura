package checker

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

	banner, err := readSSHIdentification(conn)
	if err != nil {
		return &Result{Status: "down", ResponseTime: time.Since(start).Milliseconds(), Message: fmt.Sprintf("invalid SSH banner: %v", err)}, nil
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

func readSSHIdentification(r io.Reader) (string, error) {
	// RFC 4253 permits server lines before the identification. Bound both a
	// single preamble line and the whole exchange, while retaining the deadline.
	const maxPreamble = 64 * 1024
	reader := bufio.NewReaderSize(io.LimitReader(r, maxPreamble+1), 4096)
	total := 0
	for {
		line, err := reader.ReadSlice('\n')
		if err != nil {
			return "", err
		}
		total += len(line)
		if total > maxPreamble {
			return "", fmt.Errorf("SSH preamble too large")
		}
		if !strings.HasPrefix(string(line), "SSH-") {
			continue
		}
		// The RFC limits the complete identification line, including CRLF.
		if len(line) > 255 {
			return "", fmt.Errorf("SSH identification too large")
		}
		banner := strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
		if !validSSHIdentification(banner) {
			return "", fmt.Errorf("malformed SSH identification")
		}
		return banner, nil
	}
}

func validSSHIdentification(banner string) bool {
	parts := strings.SplitN(banner, "-", 3)
	if len(parts) != 3 || parts[0] != "SSH" || strings.ContainsAny(banner, "\x00\r\n") {
		return false
	}
	major, minor, ok := strings.Cut(parts[1], ".")
	if !ok || major == "" || minor == "" {
		return false
	}
	for _, part := range []string{major, minor} {
		for _, b := range part {
			if b < '0' || b > '9' {
				return false
			}
		}
	}
	software, _, _ := strings.Cut(parts[2], " ")
	if software == "" {
		return false
	}
	for _, b := range software {
		if b < '!' || b > '~' || b == '-' {
			return false
		}
	}
	return true
}

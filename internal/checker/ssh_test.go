package checker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/v2/internal/storage"
)

func TestSSHIdentificationPreamble(t *testing.T) {
	banner := "SSH-2.0-fixture"
	sum := sha256.Sum256([]byte(banner))
	for _, tc := range []struct {
		name, status, fingerprint string
		parts                     []string
	}{
		{"plain", "up", "", []string{banner + "\r\n"}},
		{"legal preamble", "up", "", []string{"Welcome to the server\r\n" + banner + "\r\n"}},
		{"fragmented preamble", "up", "", []string{"Welcome", "\r\n\r\nSSH-2.0-", "fixture\r\n"}},
		{"preamble fingerprint", "up", hex.EncodeToString(sum[:]), []string{"Welcome\r\n" + banner + "\r\n"}},
		{"fingerprint mismatch", "down", "wrong", []string{banner + "\r\n"}},
		{"missing identification", "down", "", []string{"Welcome\r\n"}},
		{"unterminated identification", "down", "", []string{banner}},
		{"empty identification", "down", "", []string{"SSH-\r\n"}},
		{"missing version", "down", "", []string{"SSH--fixture\r\n"}},
		{"invalid version", "down", "", []string{"SSH-invalid-fixture\r\n"}},
		{"missing software", "down", "", []string{"SSH-2.0-\r\n"}},
		{"invalid software", "down", "", []string{"SSH-2.0-fixt\ture\r\n"}},
		{"NUL in comments", "down", "", []string{"SSH-2.0-fixture bad\x00comment\r\n"}},
		{"maximum identification", "up", "", []string{"SSH-2.0-" + strings.Repeat("x", 245) + "\r\n"}},
		{"oversized identification", "down", "", []string{"SSH-2.0-" + strings.Repeat("x", 246) + "\r\n"}},
		{"bounded preamble", "down", "", []string{strings.Repeat("welcome\r\n", 8192) + banner + "\r\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(3 * time.Second))
				for i, part := range tc.parts {
					if i > 0 {
						time.Sleep(30 * time.Millisecond)
					}
					if _, err := io.WriteString(conn, part); err != nil {
						return
					}
				}
			}()
			t.Cleanup(func() { ln.Close(); <-done })
			settings, err := json.Marshal(storage.SSHSettings{ExpectedFingerprint: tc.fingerprint})
			if err != nil {
				t.Fatal(err)
			}
			got, err := (&SSHChecker{AllowPrivate: true}).Check(context.Background(), &storage.Monitor{Target: ln.Addr().String(), Timeout: 1, Settings: settings})
			if err != nil || got.Status != tc.status {
				t.Fatalf("result=%+v, error=%v; want %s", got, err, tc.status)
			}
			if tc.status == "up" && tc.name != "maximum identification" && got.Message != banner {
				t.Fatalf("identification=%q, want %q", got.Message, banner)
			}
		})
	}
}

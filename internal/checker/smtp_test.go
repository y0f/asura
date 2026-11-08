package checker

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/v2/internal/storage"
)

func TestSMTPCheckerGreeting(t *testing.T) {
	for _, tc := range []struct {
		name   string
		parts  []string
		expect string
		close  bool
		want   string
	}{
		{"fragmented greeting", []string{"220 ma", "il.example ESMTP\r\n"}, "mail.example", false, "up"},
		{"multiline greeting", []string{"220-mail.example ESMTP\r\n", "220 probe-ready\r\n"}, "probe-ready", false, "up"},
		{"greeting without text", []string{"220\r\n"}, "", false, "up"},
		{"multiline final code without text", []string{"220-mail.example\r\n220\r\n"}, "mail.example", false, "up"},
		{"banner mismatch", []string{"220 mail.example\r\n"}, "other.example", false, "down"},
		{"match outside greeting", []string{"220 mail.example\r\n250 not-greeting\r\n"}, "not-greeting", false, "down"},
		{"rejected greeting", []string{"421 service unavailable\r\n"}, "", false, "down"},
		{"truncated greeting", []string{"220 mail.example"}, "mail.example", true, "down"},
		{"unterminated greeting", []string{"220 mail.example\n"}, "mail.example", false, "down"},
		{"incomplete multiline greeting", []string{"220-mail.example\r\n"}, "mail.example", false, "down"},
		{"invalid continuation", []string{"220-mail.example\r\n250 wrong code\r\n"}, "mail.example", false, "down"},
		{"maximum reply line", []string{"220 " + strings.Repeat("x", 506) + "\r\n"}, "", false, "up"},
		{"oversized reply line", []string{"220 " + strings.Repeat("x", 507) + "\r\n"}, "", false, "down"},
		{"oversized greeting", []string{strings.Repeat("220-"+strings.Repeat("x", 506)+"\r\n", 128) + "220 done\r\n"}, "", false, "down"},
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
						time.Sleep(100 * time.Millisecond)
					}
					if _, err := io.WriteString(conn, part); err != nil {
						return
					}
				}
				if tc.close {
					return
				}
				reader := bufio.NewReader(conn)
				for {
					command, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(command, "EHLO "):
						io.WriteString(conn, "250 mail.example\r\n")
					case command == "QUIT\r\n":
						io.WriteString(conn, "221 goodbye\r\n")
						return
					default:
						return
					}
				}
			}()
			t.Cleanup(func() { ln.Close(); <-done })
			settings, err := json.Marshal(storage.SMTPSettings{ExpectBanner: tc.expect})
			if err != nil {
				t.Fatal(err)
			}
			got, err := (&SMTPChecker{AllowPrivate: true}).Check(context.Background(), &storage.Monitor{
				Target: ln.Addr().String(), Timeout: 1, Settings: settings,
			})
			if err != nil || got.Status != tc.want {
				t.Fatalf("check = %+v, %v; want %s", got, err, tc.want)
			}
		})
	}
}

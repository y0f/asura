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

	"github.com/y0f/asura/v3/internal/model"
)

func protocolServer(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			serve(conn)
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			t.Error("protocol fixture did not finish")
		}
	})
	return listener.Addr().String()
}

func TestRedisCheckerRequiresBoundedCompleteReplies(t *testing.T) {
	for _, tc := range []struct {
		name, auth, ping, want string
	}{
		{"PING", "", "+PONG\r\n", "up"},
		{"AUTH and PING", "+OK\r\n", "+PONG\r\n", "up"},
		{"PONG prefix", "", "+PONG-not-valid\r\n", "down"},
		{"AUTH prefix", "+OK-not-valid\r\n", "+PONG\r\n", "down"},
		{"PONG missing CRLF", "", "+PONG\n", "down"},
		{"AUTH missing CRLF", "+OK\n", "+PONG\r\n", "down"},
		{"large PONG line", "", "+PONG" + strings.Repeat("x", 64<<10) + "\r\n", "down"},
		{"large AUTH line", "+OK" + strings.Repeat("x", 64<<10) + "\r\n", "+PONG\r\n", "down"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := protocolServer(t, func(conn net.Conn) {
				if _, err := io.WriteString(conn, tc.auth+tc.ping); err == nil {
					_, _ = io.Copy(io.Discard, conn)
				}
			})
			settings := json.RawMessage(`{}`)
			if tc.auth != "" {
				settings = json.RawMessage(`{"password":"fixture"}`)
			}
			got, err := (&RedisChecker{AllowPrivate: true}).Check(context.Background(), &model.Monitor{Target: target, Timeout: 2, Settings: settings})
			if err != nil || got == nil || got.Status != tc.want {
				t.Fatalf("result=%+v err=%v want=%s", got, err, tc.want)
			}
		})
	}
}

func TestSMTPCheckerBoundsPostGreetingResponses(t *testing.T) {
	for _, name := range []string{"long line", "many valid lines", "QUIT reply"} {
		t.Run(name, func(t *testing.T) {
			target := protocolServer(t, func(conn net.Conn) {
				_, _ = io.WriteString(conn, "220 fixture.example\r\n")
				reader := bufio.NewReader(conn)
				for {
					command, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(command, "EHLO "):
						response := "250 " + strings.Repeat("x", 1<<20) + "\r\n"
						if name == "many valid lines" {
							response = strings.Repeat("250-"+strings.Repeat("x", 500)+"\r\n", 2200) + "250 done\r\n"
						} else if name == "QUIT reply" {
							response = "250 fixture.example\r\n"
						}
						if _, err := io.WriteString(conn, response); err != nil {
							return
						}
					case strings.HasPrefix(command, "HELO "):
						_, _ = io.WriteString(conn, "250 fixture.example\r\n")
					case command == "QUIT\r\n":
						reply := "221 goodbye\r\n"
						if name == "QUIT reply" {
							reply = "221 " + strings.Repeat("x", 1<<20) + "\r\n"
						}
						_, _ = io.WriteString(conn, reply)
						return
					default:
						return
					}
				}
			})
			got, err := (&SMTPChecker{AllowPrivate: true}).Check(context.Background(), &model.Monitor{Target: target, Timeout: 2})
			if err != nil || got == nil || got.Status != "down" || !strings.Contains(got.Message, "exceeds 1 MiB") {
				t.Fatalf("post-greeting traffic not bounded: %+v %v", got, err)
			}
		})
	}
}

func TestRedisAndSMTPConnectedChecksCancel(t *testing.T) {
	for _, c := range []Checker{&RedisChecker{AllowPrivate: true}, &SMTPChecker{AllowPrivate: true}} {
		t.Run(c.Type(), func(t *testing.T) {
			checkQuietSocketCancellation(t, func(ctx context.Context, target string) (*Result, error) {
				return c.Check(ctx, &model.Monitor{Target: target, Timeout: 30})
			})
		})
	}
}

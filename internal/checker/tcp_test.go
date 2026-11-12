package checker

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/v3/internal/storage"
)

func TestTCPCheckerExpectedData(t *testing.T) {
	for _, tc := range []struct {
		name     string
		parts    []string
		keepOpen bool
		want     string
	}{
		{"fragmented match", []string{"banner HE", "LLO world"}, false, "up"},
		{"match without EOF", []string{"HELLO"}, true, "up"},
		{"no match before EOF", []string{"goodbye"}, false, "down"},
		{"incomplete match timeout", []string{"HE"}, true, "down"},
		{"match at read limit", []string{strings.Repeat("x", 4091), "HELLO"}, false, "up"},
		{"match beyond read limit", []string{strings.Repeat("x", 4096) + "HELLO"}, false, "down"},
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
				if tc.keepOpen {
					io.Copy(io.Discard, conn)
				}
			}()
			t.Cleanup(func() { ln.Close(); <-done })
			settings, err := json.Marshal(storage.TCPSettings{ExpectData: "HELLO"})
			if err != nil {
				t.Fatal(err)
			}
			got, err := (&TCPChecker{AllowPrivate: true}).Check(context.Background(), &storage.Monitor{
				Target: ln.Addr().String(), Timeout: 1, Settings: settings,
			})
			if err != nil || got.Status != tc.want {
				t.Fatalf("check = %+v, %v; want %s", got, err, tc.want)
			}
		})
	}
}

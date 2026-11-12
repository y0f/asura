package checker

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/v3/internal/storage"
)

func pgTestFrame(kind byte, payload string) []byte {
	frame := make([]byte, 5+len(payload))
	frame[0] = kind
	binary.BigEndian.PutUint32(frame[1:5], uint32(4+len(payload)))
	copy(frame[5:], payload)
	return frame
}

func TestPostgreSQLResponseFraming(t *testing.T) {
	auth := pgTestFrame('R', "\x00\x00\x00\x00")
	errFrame := pgTestFrame('E', "SFATAL\x00C57P03\x00Mdatabase starting\x00\x00")
	longMessage := strings.Repeat("x", 2048)
	headerMMessage := strings.Repeat("x", 56) // Length byte is 'M', not a field.
	for _, tc := range []struct {
		name    string
		parts   [][]byte
		status  string
		message string
	}{
		{"authentication", [][]byte{auth}, "up", "PostgreSQL accepting connections"},
		{"fragmented authentication", [][]byte{auth[:1], auth[1:6], auth[6:]}, "up", "PostgreSQL accepting connections"},
		{"fragmented error", [][]byte{errFrame[:2], errFrame[2:8], errFrame[8:]}, "up", "PostgreSQL up: database starting"},
		{"error over old read size", [][]byte{pgTestFrame('E', "SFATAL\x00C57P03\x00M"+longMessage+"\x00\x00")}, "up", "PostgreSQL up: " + longMessage},
		{"message field ignores header", [][]byte{pgTestFrame('E', "SFATAL\x00C57P03\x00M"+headerMMessage+"\x00\x00")}, "up", "PostgreSQL up: " + headerMMessage},
		{"MD5 challenge", [][]byte{pgTestFrame('R', "\x00\x00\x00\x05salt")}, "up", ""},
		{"SASL challenge", [][]byte{pgTestFrame('R', "\x00\x00\x00\x0aSCRAM-SHA-256\x00\x00")}, "up", ""},
		{"MD5 salt missing", [][]byte{pgTestFrame('R', "\x00\x00\x00\x05")}, "down", ""},
		{"unknown authentication code", [][]byte{pgTestFrame('R', "\x00\x00\x00\xff")}, "down", ""},
		{"authentication extra bytes", [][]byte{pgTestFrame('R', "\x00\x00\x00\x00extra")}, "down", ""},
		{"unterminated SASL list", [][]byte{pgTestFrame('R', "\x00\x00\x00\x0aSCRAM-SHA-256\x00")}, "down", ""},
		{"truncated header", [][]byte{{'R'}}, "down", ""},
		{"truncated payload", [][]byte{auth[:7]}, "down", ""},
		{"short authentication", [][]byte{pgTestFrame('R', "")}, "down", ""},
		{"invalid frame length", [][]byte{{'R', 0, 0, 0, 3}}, "down", ""},
		{"oversized frame", [][]byte{{'R', 0x7f, 0xff, 0xff, 0xff}}, "down", ""},
		{"unterminated error field", [][]byte{pgTestFrame('E', "Mbroken")}, "down", ""},
		{"missing error terminator", [][]byte{pgTestFrame('E', "Mbroken\x00")}, "down", ""},
		{"empty error", [][]byte{pgTestFrame('E', "\x00")}, "down", ""},
		{"missing severity", [][]byte{pgTestFrame('E', "C57P03\x00Mstarting\x00\x00")}, "down", ""},
		{"missing SQLSTATE", [][]byte{pgTestFrame('E', "SFATAL\x00Mstarting\x00\x00")}, "down", ""},
		{"missing message", [][]byte{pgTestFrame('E', "SFATAL\x00C57P03\x00\x00")}, "down", ""},
		{"invalid SQLSTATE", [][]byte{pgTestFrame('E', "SFATAL\x00Cinvalid\x00Mstarting\x00\x00")}, "down", ""},
		{"unexpected message", [][]byte{pgTestFrame('Z', "I")}, "down", ""},
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
				var length [4]byte
				if _, err := io.ReadFull(conn, length[:]); err != nil {
					return
				}
				if _, err := io.CopyN(io.Discard, conn, int64(binary.BigEndian.Uint32(length[:]))-4); err != nil {
					return
				}
				for i, part := range tc.parts {
					if i > 0 {
						time.Sleep(30 * time.Millisecond)
					}
					if _, err := conn.Write(part); err != nil {
						return
					}
				}
			}()
			t.Cleanup(func() { ln.Close(); <-done })
			result, err := (&PostgreSQLChecker{AllowPrivate: true}).Check(context.Background(), &storage.Monitor{Target: ln.Addr().String(), Timeout: 1})
			if err != nil || result.Status != tc.status {
				t.Fatalf("result=%+v, error=%v; want %s", result, err, tc.status)
			}
			if tc.message != "" && result.Message != tc.message {
				t.Fatalf("message=%q, want %q", result.Message, tc.message)
			}
		})
	}
}

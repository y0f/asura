package checker

import (
	"bufio"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
)

func TestRedisReplyExactByteLimit(t *testing.T) {
	for _, n := range []int{maxRedisReplyBytes - 1, maxRedisReplyBytes, maxRedisReplyBytes + 1} {
		input := "+" + strings.Repeat("x", n-3) + "\r\n"
		line, err := readRedisReply(bufio.NewReaderSize(strings.NewReader(input), maxRedisReplyBytes))
		if n <= maxRedisReplyBytes {
			if err != nil || len(line) != n-2 {
				t.Fatalf("legal %d-byte line rejected: len=%d err=%v", n, len(line), err)
			}
		} else if err == nil || line != "" {
			t.Fatal("oversized line returned partial data")
		}
	}
}

type smtpReadFixture struct {
	net.Conn
	io.Reader
	// Only Read is called by the byte-budget unit test.
}

func (c smtpReadFixture) Read(p []byte) (int, error) { return c.Reader.Read(p) }

func TestSMTPReadBudgetNeverBecomesTruncatedEOF(t *testing.T) {
	// A net.Pipe is unnecessary here: the production wrapper delegates Read to
	// the embedded connection; the adapter only substitutes its byte source.
	conn := &smtpResponseConn{Conn: smtpReadFixture{Reader: strings.NewReader("12345")}, remaining: 4}
	buf := make([]byte, 8)
	n, err := conn.Read(buf)
	if err != nil || n != 4 || string(buf[:n]) != "1234" {
		t.Fatalf("exact budget read=%d %q %v", n, buf[:n], err)
	}
	n, err = conn.Read(buf)
	if n != 0 || err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("exhausted budget looked like successful/truncated EOF: %d %v", n, err)
	}
	if conn.check(nil) == nil {
		t.Fatal("protocol parser could swallow the limit error")
	}
	if n, err := conn.Write([]byte("HELO fallback\r\n")); n != 0 || err == nil {
		t.Fatal("protocol continued writing after exhausting its response budget")
	}
}

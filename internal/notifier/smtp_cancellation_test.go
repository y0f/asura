package notifier

import (
	"bufio"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestSMTPContextCancellationInterruptsStalledCommand(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(conn, "220 fixture ESMTP\r\n")
		if _, err := bufio.NewReader(conn).ReadString('\n'); err != nil {
			return
		}
		close(accepted)
		_, _ = io.Copy(io.Discard, conn)
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, err := newSMTPClient(ctx, ln.Addr().String(), "localhost")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	done := make(chan error, 1)
	go func() {
		done <- client.Hello("localhost")
	}()
	<-accepted
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stalled command unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP ignored context cancellation after dial")
	}
	<-stopped
}

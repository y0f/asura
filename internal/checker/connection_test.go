package checker

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/y0f/asura/v3/internal/model"
)

type canceledCheck struct {
	result *Result
	err    error
}

func TestConnectedChecksCancelBeforeSocketTimeout(t *testing.T) {
	for _, tc := range []struct {
		checker  Checker
		settings string
	}{
		{&TCPChecker{AllowPrivate: true}, `{"send_data":"probe","expect_data":"reply"}`},
		{&PostgreSQLChecker{AllowPrivate: true}, `{}`},
		{&MQTTChecker{AllowPrivate: true}, `{}`},
		{&SSHChecker{AllowPrivate: true}, `{}`},
		{&TLSChecker{AllowPrivate: true}, `{}`},
	} {
		t.Run(tc.checker.Type(), func(t *testing.T) {
			checkQuietSocketCancellation(t, func(ctx context.Context, target string) (*Result, error) {
				return tc.checker.Check(ctx, &model.Monitor{Target: target, Timeout: 30, Settings: json.RawMessage(tc.settings)})
			})
		})
	}
	t.Run("whois", func(t *testing.T) {
		checkQuietSocketCancellation(t, func(ctx context.Context, target string) (*Result, error) {
			dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", target)
			}
			_, _, err := queryWhois(ctx, "example.test", "fixture", 30*time.Second, dial)
			return nil, err
		})
	})
}

func checkQuietSocketCancellation(t *testing.T, check func(context.Context, string) (*Result, error)) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan canceledCheck, 1)
	go func() { result, err := check(ctx, listener.Addr().String()); done <- canceledCheck{result, err} }()
	var peer net.Conn
	select {
	case peer = <-accepted:
	case got := <-done:
		t.Fatalf("check ended before connecting: %+v", got)
	case <-time.After(5 * time.Second):
		t.Fatal("checker did not connect to quiet fixture")
	}
	defer peer.Close()
	cancel()
	select {
	case got := <-done:
		if got.err == nil && (got.result == nil || got.result.Status == "up") {
			t.Errorf("cancelled quiet check reported success: %+v", got)
		}
	case <-time.After(time.Second):
		t.Error("connected check ignored cancellation until its socket timeout")
		peer.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("fixture close did not release check")
		}
	}
}

func TestConnectedUDPCheckCancelsBeforeSocketTimeout(t *testing.T) {
	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan canceledCheck, 1)
	go func() {
		result, err := (&UDPChecker{AllowPrivate: true}).Check(ctx, &model.Monitor{
			Target: peer.LocalAddr().String(), Timeout: 30, Settings: json.RawMessage(`{"send_data":"probe","expect_data":"reply"}`),
		})
		done <- canceledCheck{result, err}
	}()
	peer.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 16)
	_, client, err := peer.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case got := <-done:
		if got.err == nil && (got.result == nil || got.result.Status == "up") {
			t.Errorf("cancelled UDP check reported success: %+v", got)
		}
	case <-time.After(time.Second):
		t.Error("connected UDP check ignored cancellation until its socket timeout")
		// Release the baseline's read without waiting for its 30-second timeout.
		peer.WriteTo([]byte("reply"), client)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("fixture response did not release UDP check")
		}
	}
}

package checker

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/y0f/asura/internal/model"
)

func TestWebSocketProbeDoesNotWaitForPeerClose(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		<-release // Deliberately never read or reply to a close frame.
	}))
	defer func() { unblock(); srv.Close() }()
	done := make(chan *Result, 1)
	go func() {
		result, _ := (&WebSocketChecker{AllowPrivate: true}).Check(context.Background(), &model.Monitor{Target: srv.URL, Timeout: 1})
		done <- result
	}()
	select {
	case result := <-done:
		if result == nil || result.Status != "up" {
			t.Fatalf("valid handshake failed: %+v", result)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Error("completed probe exceeded its timeout waiting for a close acknowledgement")
		unblock()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("peer close did not release checker")
		}
	}
}

func TestWebSocketRejectedUpgradeReleasesIdleConnection(t *testing.T) {
	closed := make(chan struct{}, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fixture rejection", http.StatusUnauthorized)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			select {
			case closed <- struct{}{}:
			default:
			}
		}
	}
	srv.Start()
	defer srv.Close()
	result, err := (&WebSocketChecker{AllowPrivate: true}).Check(context.Background(), &model.Monitor{Target: srv.URL, Timeout: 2})
	if err != nil || result.Status != "down" {
		t.Fatalf("rejected upgrade reported success: %+v %v", result, err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Error("per-check transport retained its idle connection after a rejected upgrade")
	}
}

func TestWebSocketRedirectsAreBoundedAndStillSupportShortChains(t *testing.T) {
	for _, loop := range []bool{false, true} {
		t.Run(map[bool]string{false: "short chain", true: "loop"}[loop], func(t *testing.T) {
			var hits atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hit := hits.Add(1)
				if loop && hit <= 12 || !loop && hit == 1 {
					http.Redirect(w, r, "/next", http.StatusFound)
					return
				}
				if loop {
					http.Error(w, "stop the unfixed loop fixture", http.StatusBadRequest)
					return
				}
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
			}))
			defer srv.Close()
			result, err := (&WebSocketChecker{AllowPrivate: true}).Check(context.Background(), &model.Monitor{Target: srv.URL, Timeout: 3})
			if loop {
				if err != nil || result.Status != "down" || hits.Load() != 10 || !strings.Contains(result.Message, "stopped after 10 redirects") {
					t.Errorf("redirect ceiling missing: hits=%d result=%+v err=%v", hits.Load(), result, err)
				}
			} else if err != nil || result.Status != "up" || hits.Load() != 2 {
				t.Errorf("ordinary redirect handshake failed: hits=%d result=%+v err=%v", hits.Load(), result, err)
			}
		})
	}
}

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
	"github.com/y0f/asura/v2/internal/model"
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
	for _, tc := range []struct {
		name         string
		hops         int64
		wantStatus   string
		wantRequests int64
	}{
		{name: "short chain", hops: 1, wantStatus: "up", wantRequests: 2},
		{name: "exact limit", hops: 10, wantStatus: "up", wantRequests: 11},
		{name: "exceeds limit", hops: 11, wantStatus: "down", wantRequests: 11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hit := hits.Add(1)
				if hit <= tc.hops {
					http.Redirect(w, r, "/next", http.StatusFound)
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
			if err != nil || result == nil || result.Status != tc.wantStatus || hits.Load() != tc.wantRequests {
				t.Fatalf("hits=%d result=%+v err=%v; want %d requests and %s", hits.Load(), result, err, tc.wantRequests, tc.wantStatus)
			}
			if tc.wantStatus == "down" && !strings.Contains(result.Message, "stopped after 10 redirects") {
				t.Errorf("missing redirect ceiling diagnostic: %+v", result)
			}
		})
	}
}

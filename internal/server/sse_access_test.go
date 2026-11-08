package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/y0f/asura/v2/internal/config"
	"github.com/y0f/asura/v2/internal/storage"
)

func TestSSEAdmissionAcceptsEachReadCapability(t *testing.T) {
	for _, base := range []string{"", "/console"} {
		for _, noWeb := range []bool{false, true} {
			srv, _ := regressionServer(t, func(cfg *config.Config) {
				cfg.Server.BasePath = base
				cfg.Server.RateLimitBurst = 1000
				if noWeb {
					headless(cfg)
				}
			})
			t.Cleanup(srv.Close)
			var cookie *http.Cookie
			if !noWeb {
				cookie = webLoginCookie(t, srv, regressionAdminKey)
			}
			key := &srv.cfg.Auth.APIKeys[0]
			key.SuperAdmin = false
			for _, permission := range []string{"", "monitors.read", "incidents.read", "sla.read", "monitors.update", "notifications.read"} {
				key.Permissions = nil
				if permission != "" {
					key.Permissions = []string{permission}
				}
				for _, web := range []bool{false, true} {
					if web && noWeb {
						continue
					}
					path := base + "/api/v1/events"
					if web {
						path = base + "/events"
					}
					r := httptest.NewRequest("GET", path, nil)
					if web {
						r.AddCookie(cookie)
					} else {
						r.Header.Set("X-API-Key", regressionAdminKey)
					}
					ctx, cancel := context.WithCancel(r.Context())
					w := httptest.NewRecorder()
					srv.ServeHTTP(&cancelOnFlush{w, cancel}, r.WithContext(ctx))
					cancel()
					want := http.StatusForbidden
					if permission == "monitors.read" || permission == "incidents.read" || permission == "sla.read" {
						want = http.StatusOK
					}
					if w.Code != want {
						t.Fatalf("base=%q headless=%v web=%v permission=%q: got %d want %d", base, noWeb, web, permission, w.Code, want)
					}
				}
			}
		}
	}
}

func TestSSEQueuedEventsRespectLogoutAndGroupRevocation(t *testing.T) {
	for _, logout := range []bool{false, true} {
		srv, store := regressionServer(t, nil)
		ctx := context.Background()
		group := &storage.MonitorGroup{Name: "stream group"}
		if err := store.CreateMonitorGroup(ctx, group); err != nil {
			t.Fatal(err)
		}
		monitors := []*storage.Monitor{{Name: "revoked", Type: "http", GroupID: &group.ID, Interval: 60, Timeout: 10}, {Name: "allowed", Type: "http", GroupID: &group.ID, Interval: 60, Timeout: 10}}
		for _, m := range monitors {
			if err := store.CreateMonitor(ctx, m); err != nil {
				t.Fatal(err)
			}
		}
		cookie := webLoginCookie(t, srv, regressionAdminKey)
		srv.cfg.Auth.Roles = []config.RoleConfig{{Name: "reader", Permissions: []string{"monitors.read"}}}
		srv.cfg.Auth.APIKeys[0].SuperAdmin = false
		srv.cfg.Auth.APIKeys[0].RoleBindings = []config.RoleBindingConfig{{Role: "reader", Scope: config.MonitorAccess{GroupIDs: []int64{group.ID}}}}
		if err := srv.cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		resume := sync.OnceFunc(func() { close(release) })
		defer resume()
		var once sync.Once
		authorize := srv.events.authorize
		srv.events.authorize = func(r *http.Request, event *SSEEvent) (bool, error) {
			if event != nil {
				once.Do(func() {
					close(entered)
					select {
					case <-release:
					case <-r.Context().Done():
					}
				})
			}
			return authorize(r, event)
		}
		ts := httptest.NewServer(srv)
		defer ts.Close()
		defer srv.Close()
		requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		r, _ := http.NewRequestWithContext(requestCtx, "GET", ts.URL+"/events", nil)
		r.AddCookie(cookie)
		response, err := ts.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("subscription: %d", response.StatusCode)
		}
		srv.events.Broadcast(SSEEvent{Type: "content.changed", MonitorID: monitors[0].ID})
		select {
		case <-entered:
		case <-requestCtx.Done():
			t.Fatal("queued frame was not consumed")
		}
		if logout {
			_, err = store.DeleteSessionsByAPIKeyName(ctx, "admin")
		} else {
			monitors[0].GroupID = nil
			err = store.UpdateMonitor(ctx, monitors[0])
		}
		resume()
		if err != nil {
			t.Fatal(err)
		}
		if logout {
			body, err := io.ReadAll(response.Body)
			if err != nil || len(body) != 0 {
				t.Fatalf("logout emitted queued data or kept stream alive: %q, %v", body, err)
			}
		} else {
			srv.events.Broadcast(SSEEvent{Type: "content.changed", MonitorID: monitors[1].ID})
			reader := bufio.NewReader(response.Body)
			if _, err := reader.ReadString('\n'); err != nil {
				t.Fatal(err)
			}
			line, err := reader.ReadString('\n')
			var event SSEEvent
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil || event.MonitorID != monitors[1].ID {
				t.Fatalf("queued event used revoked scope: %s, %v", line, err)
			}
		}
		response.Body.Close()
	}
}

func TestSSEHeartbeatRevalidatesIdentity(t *testing.T) {
	broker := NewEventBroker(nil)
	broker.heartbeatInterval = time.Millisecond
	var checks atomic.Int64
	broker.authorize = func(*http.Request, *SSEEvent) (bool, error) {
		if checks.Add(1) == 1 {
			return true, nil
		}
		return false, errStreamIdentity
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	w := httptest.NewRecorder()
	broker.ServeHTTP(w, httptest.NewRequest("GET", "/events", nil).WithContext(ctx))
	if checks.Load() != 2 || w.Body.Len() != 0 || broker.ClientCount() != 0 {
		t.Fatalf("heartbeat did not terminate revoked identity: checks=%d body=%q clients=%d", checks.Load(), w.Body.String(), broker.ClientCount())
	}
}

func TestSSECurrentScopesAndSessionIdentity(t *testing.T) {
	srv, store := regressionServer(t, nil)
	defer srv.Close()
	ctx := context.Background()
	g := &storage.MonitorGroup{Name: "incident readers"}
	if err := store.CreateMonitorGroup(ctx, g); err != nil {
		t.Fatal(err)
	}
	monitors := []*storage.Monitor{{Name: "incidents only", Type: "http", GroupID: &g.ID, Interval: 60, Timeout: 10}, {Name: "monitor only", Type: "http", Interval: 60, Timeout: 10}}
	for _, m := range monitors {
		if err := store.CreateMonitor(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	srv.cfg.Auth.Roles = []config.RoleConfig{{Name: "incidents", Permissions: []string{"incidents.read"}}, {Name: "monitors", Permissions: []string{"monitors.read"}}}
	srv.cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "scoped", Hash: config.HashAPIKey("scoped"), RoleBindings: []config.RoleBindingConfig{
		{Role: "incidents", Scope: config.MonitorAccess{GroupIDs: []int64{g.ID}}},
		{Role: "monitors", Scope: config.MonitorAccess{MonitorIDs: []int64{monitors[1].ID}}},
	}}}
	if err := srv.cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	identity := streamIdentity{name: "scoped", keyHash: srv.cfg.Auth.APIKeys[0].Hash}
	request := func(identity streamIdentity) *http.Request {
		return httptest.NewRequest("GET", "/events", nil).WithContext(context.WithValue(ctx, streamIdentityKey{}, identity))
	}
	r := request(identity)
	for _, tc := range []struct {
		event   SSEEvent
		allowed bool
	}{
		{SSEEvent{Type: "incident.created", MonitorID: monitors[0].ID, IncidentID: 1}, true},
		{SSEEvent{Type: "content.changed", MonitorID: monitors[0].ID}, false},
		{SSEEvent{Type: "incident.created", MonitorID: monitors[1].ID, IncidentID: 2}, false},
		{SSEEvent{Type: "content.changed", MonitorID: monitors[1].ID}, true},
		{SSEEvent{Type: "sla.breach", MonitorID: monitors[1].ID}, false},
	} {
		allowed, err := srv.authorizeEvent(r, &tc.event)
		if err != nil || allowed != tc.allowed {
			t.Fatalf("%+v: allowed=%v err=%v", tc.event, allowed, err)
		}
	}
	monitors[0].GroupID = nil
	if err := store.UpdateMonitor(ctx, monitors[0]); err != nil {
		t.Fatal(err)
	}
	if allowed, err := srv.authorizeEvent(r, &SSEEvent{Type: "incident.created", MonitorID: monitors[0].ID, IncidentID: 1}); err != nil || allowed {
		t.Fatalf("old group grant survived move: %v, %v", allowed, err)
	}
	session := &storage.Session{TokenHash: "stream-test-session", APIKeyName: identity.name, KeyHash: identity.keyHash, ExpiresAt: time.Now().Add(time.Hour)}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	identity.sessionHash = session.TokenHash
	r = request(identity)
	if allowed, err := srv.authorizeEvent(r, nil); err != nil || !allowed {
		t.Fatalf("valid session: %v, %v", allowed, err)
	}
	if err := store.DeleteSession(ctx, session.TokenHash); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.authorizeEvent(r, nil); !errors.Is(err, errStreamIdentity) {
		t.Fatalf("logout kept stream alive: %v", err)
	}
	identity.sessionHash = ""
	r = request(identity)
	srv.cfg.Auth.APIKeys[0].Hash = config.HashAPIKey("rotated")
	if _, err := srv.authorizeEvent(r, nil); !errors.Is(err, errStreamIdentity) {
		t.Fatalf("same-name rotation kept stream alive: %v", err)
	}
	srv.cfg.Auth.APIKeys = nil
	if _, err := srv.authorizeEvent(r, nil); !errors.Is(err, errStreamIdentity) {
		t.Fatalf("removed principal kept stream alive: %v", err)
	}
}

func TestSSEFramesContainOnlyTypedMetadata(t *testing.T) {
	broker := NewEventBroker(nil)
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)
	broker.Broadcast(SSEEvent{Type: "incident.created", MonitorID: 7, IncidentID: 9, Status: "private-secret"})
	frame := <-client.ch
	var payload map[string]any
	line := strings.Split(string(frame.data), "\n")[1]
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 2 || payload["monitor_id"] != float64(7) || payload["incident_id"] != float64(9) || strings.Contains(string(frame.data), "private-secret") {
		t.Fatalf("unexpected stream payload: %s", frame.data)
	}
	broker.Broadcast(SSEEvent{Type: "content.changed", MonitorID: 7, IncidentID: 9, Status: "open"})
	if frame = <-client.ch; strings.Contains(string(frame.data), "incident_id") || strings.Contains(string(frame.data), "status") {
		t.Fatalf("monitor event exposed incident metadata: %s", frame.data)
	}
	for _, event := range []SSEEvent{{Type: "unknown\ndata: injected", MonitorID: 1}, {Type: "content.changed"}, {Type: "incident.created", MonitorID: 1}} {
		broker.Broadcast(event)
	}
	select {
	case frame := <-client.ch:
		t.Fatalf("invalid event broadcast: %s", frame.data)
	default:
	}
}

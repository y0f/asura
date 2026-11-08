package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/notifier"
	"github.com/y0f/asura/internal/storage"
)

const regressionAdminKey = "regression-admin-key"

func regressionServer(t *testing.T, mutate func(*config.Config)) (*Server, storage.Store) {
	t.Helper()
	f, err := os.CreateTemp("", "asura-server-regression-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })
	store, err := storage.NewSQLiteStore(f.Name(), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	cfg := config.Defaults()
	cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "admin", Hash: config.HashAPIKey(regressionAdminKey), SuperAdmin: true}}
	if mutate != nil {
		mutate(cfg)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := notifier.NewDispatcher(store, logger, true)
	t.Cleanup(func() { dispatcher.Shutdown(context.Background()) })
	return NewServer(cfg, store, nil, dispatcher, nil, logger, "test"), store
}

func headless(c *config.Config) {
	f := false
	c.Server.WebUIEnabled = &f
}

func waitClients(t *testing.T, b *EventBroker, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for b.ClientCount() < n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d SSE clients (have %d)", n, b.ClientCount())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func openEventStream(t *testing.T, url string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", url+"/api/v1/events", nil)
	req.Header.Set("X-API-Key", regressionAdminKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events: status %d", resp.StatusCode)
	}
	return resp
}

// The server-wide WriteTimeout (30s by default, equal to the heartbeat) must
// not cut SSE streams.
func TestSSESurvivesServerWriteTimeout(t *testing.T) {
	srv, store := regressionServer(t, headless)
	mon := &storage.Monitor{Name: "stream monitor", Type: "http", Interval: 60, Timeout: 10}
	if err := store.CreateMonitor(context.Background(), mon); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(srv)
	ts.Config.WriteTimeout = 1 * time.Second
	ts.Start()
	defer ts.Close()
	defer srv.EventBroker().Close() // end the stream so ts.Close does not wait on it

	resp := openEventStream(t, ts.URL)
	waitClients(t, srv.EventBroker(), 1)

	lines := make(chan string, 16)
	readErr := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		readErr <- sc.Err()
		close(lines)
	}()

	time.Sleep(1500 * time.Millisecond)
	srv.EventBroker().Broadcast(SSEEvent{Type: "incident.created", MonitorID: mon.ID, IncidentID: 1})

	timeout := time.After(2 * time.Second)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("SSE stream closed after write_timeout before the event arrived (read err: %v)", <-readErr)
			}
			if strings.HasPrefix(l, "event: incident.created") {
				return
			}
		case <-timeout:
			t.Fatal("event not delivered")
		}
	}
}

// Graceful shutdown must not wait for open SSE streams (main registers
// EventBroker.Close with RegisterOnShutdown).
func TestShutdownClosesSSEStreams(t *testing.T) {
	srv, _ := regressionServer(t, headless)
	ts := httptest.NewUnstartedServer(srv)
	ts.Config.WriteTimeout = 0
	ts.Config.RegisterOnShutdown(srv.EventBroker().Close)
	ts.Start()
	defer ts.Close()

	openEventStream(t, ts.URL)
	waitClients(t, srv.EventBroker(), 1)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := ts.Config.Shutdown(ctx); errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Shutdown blocked %s on one open SSE stream; SSE clients still connected: %d",
			time.Since(start).Round(100*time.Millisecond), srv.EventBroker().ClientCount())
	}
	if srv.EventBroker().Subscribe() != nil {
		t.Error("broker accepted a new client after Close")
	}
}

// Headless, the confirm/unsubscribe links in subscriber emails must work and
// the API must expose subscriber IDs for DELETE.
func TestHeadlessSubscriberLinks(t *testing.T) {
	srv, store := regressionServer(t, func(c *config.Config) {
		headless(c)
		c.Subscriptions.Enabled = true
	})
	ctx := context.Background()
	sp := &storage.StatusPage{Slug: "s1", Title: "S1", Enabled: true, APIEnabled: true}
	if err := store.CreateStatusPage(ctx, sp); err != nil {
		t.Fatal(err)
	}
	srv.refreshStatusSlugs()
	sub := &storage.StatusPageSubscriber{StatusPageID: sp.ID, Type: "email", Email: "a@example.com"}
	if err := store.CreateStatusPageSubscriber(ctx, sub); err != nil {
		t.Fatal(err)
	}

	// Confirmation link (notifier.SendConfirmationEmail).
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/s1/confirm?token="+sub.Token, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("confirm link: %d %s", w.Code, w.Body.String())
	}
	if got, err := store.GetSubscriberByToken(ctx, sub.Token); err != nil || !got.Confirmed {
		t.Fatalf("subscriber not confirmed: %+v %v", got, err)
	}
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/s1/confirm?token=bogus", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("bogus confirm token: got %d, want 404", w.Code)
	}

	// The subscriber list exposes IDs.
	req := httptest.NewRequest("GET", "/api/v1/status-pages/1/subscribers", nil)
	req.Header.Set("X-API-Key", regressionAdminKey)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	var body struct {
		Count int64                           `json:"count"`
		Data  []*storage.StatusPageSubscriber `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Count != 1 || len(body.Data) != 1 || body.Data[0].ID != sub.ID {
		t.Errorf("subscribers list = %s; want count 1 and subscriber %d", strings.TrimSpace(w.Body.String()), sub.ID)
	}

	// Unsubscribe link (notifier subscriber emails).
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/s1/unsubscribe?token="+sub.Token, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("unsubscribe link: %d %s", w.Code, w.Body.String())
	}
	if _, err := store.GetSubscriberByToken(ctx, sub.Token); err == nil {
		t.Error("subscriber still present after unsubscribe")
	}
}

// Headless there is no dashboard, so {base_path} must not redirect to it.
func TestHeadlessBasePathNoRedirect(t *testing.T) {
	srv, _ := regressionServer(t, func(c *config.Config) {
		headless(c)
		c.Server.BasePath = "/asura"
	})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/asura", nil))
	if w.Code == http.StatusMovedPermanently {
		t.Errorf("headless: GET /asura -> 301 %s, which is a 404", w.Header().Get("Location"))
	}
}

// Unknown API routes and wrong methods answer with JSON errors.
func TestAPIUnknownRouteJSON(t *testing.T) {
	srv, _ := regressionServer(t, headless)
	for _, tc := range []struct {
		method, path string
		code         int
	}{
		{"GET", "/api/v1/does-not-exist", http.StatusNotFound},
		{"PATCH", "/api/v1/monitors/1", http.StatusMethodNotAllowed},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("X-API-Key", regressionAdminKey)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		var body map[string]any
		jsonErr := json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != tc.code || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || jsonErr != nil || body["error"] == nil {
			t.Errorf("%s %s -> %d Content-Type=%q body=%q; want %d JSON {\"error\":...}",
				tc.method, tc.path, w.Code, w.Header().Get("Content-Type"), strings.TrimSpace(w.Body.String()), tc.code)
		}
		if tc.code == http.StatusMethodNotAllowed && w.Header().Get("Allow") == "" {
			t.Errorf("%s %s: missing Allow header", tc.method, tc.path)
		}
	}
}

type slowLogStore struct {
	storage.Store
	mu       sync.Mutex
	inserted int
}

func (s *slowLogStore) InsertRequestLogBatch(ctx context.Context, logs []*storage.RequestLog) error {
	time.Sleep(200 * time.Millisecond)
	s.mu.Lock()
	s.inserted += len(logs)
	s.mu.Unlock()
	return nil
}

// RequestLogWriter.Run must persist logs still queued when ctx is cancelled.
func TestRequestLogWriterDrainsQueueOnCancel(t *testing.T) {
	st := &slowLogStore{}
	w := NewRequestLogWriter(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	const n = 500
	for i := 0; i < n; i++ {
		w.Send(&storage.RequestLog{Method: "GET", Path: "/x", StatusCode: 200, CreatedAt: time.Now()})
	}
	time.Sleep(50 * time.Millisecond) // first batch insert is in flight
	cancel()
	<-done
	st.mu.Lock()
	got := st.inserted
	st.mu.Unlock()
	if got != n {
		t.Errorf("RequestLogWriter.Run persisted %d of %d queued logs", got, n)
	}
}

type countingFlushWriter struct {
	h  http.Header
	wg *sync.WaitGroup
}

func (c *countingFlushWriter) Header() http.Header { return c.h }
func (c *countingFlushWriter) WriteHeader(int)     {}
func (c *countingFlushWriter) Flush()              {}
func (c *countingFlushWriter) Write(p []byte) (int, error) {
	if len(p) > 6 && string(p[:6]) == "event:" {
		c.wg.Done()
	}
	return len(p), nil
}

// BenchmarkSSEFanout measures one Broadcast of a realistic payload to 100
// clients; the event is marshalled once, not once per client.
func BenchmarkSSEFanout(b *testing.B) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broker := NewEventBroker(logger)
	// This benchmark isolates encoding and fan-out; authorization has separate tests.
	broker.authorize = func(*http.Request, *SSEEvent) (bool, error) { return true, nil }
	const clients = 100
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < clients; i++ {
		req := httptest.NewRequest("GET", "/events", nil).WithContext(ctx)
		go broker.ServeHTTP(&countingFlushWriter{h: http.Header{}, wg: &wg}, req)
	}
	for broker.ClientCount() < clients {
		time.Sleep(time.Millisecond)
	}
	event := SSEEvent{Type: "incident.created", MonitorID: 1, IncidentID: 1, Status: "open"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wg.Add(clients)
		broker.Broadcast(event)
		wg.Wait()
	}
}

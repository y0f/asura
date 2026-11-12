package notifier

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/storage"
)

// The SSRF guard must also block [::], which reaches localhost on Linux.
func TestWebhookCannotReachLocalhostViaUnspecifiedV6(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("connect() to [::] reaching loopback is Linux behaviour")
	}
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback:", err)
	}
	var hit atomic.Bool
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit.Store(true) })}
	go srv.Serve(ln)
	defer srv.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	settings, _ := json.Marshal(WebhookSettings{URL: fmt.Sprintf("http://[::]:%d/", port)})
	err = (&WebhookSender{AllowPrivate: false}).Send(context.Background(),
		&storage.NotificationChannel{ID: 1, Type: "webhook", Settings: settings}, &Payload{EventType: "incident.created"})
	if err == nil || hit.Load() {
		t.Fatalf("webhook with AllowPrivate=false reached a loopback-only listener via [::] (err=%v, hit=%v)", err, hit.Load())
	}
}

type historyStore struct {
	storage.Store
	channels []*storage.NotificationChannel
	mu       sync.Mutex
	history  []*storage.NotificationHistory
}

func (s *historyStore) ListNotificationChannels(context.Context) ([]*storage.NotificationChannel, error) {
	return s.channels, nil
}

func (s *historyStore) InsertNotificationHistory(_ context.Context, h *storage.NotificationHistory) error {
	s.mu.Lock()
	s.history = append(s.history, h)
	s.mu.Unlock()
	return nil
}

func (s *historyStore) snapshot() []*storage.NotificationHistory {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*storage.NotificationHistory(nil), s.history...)
}

// blockingSender blocks every Send until its context is done.
type blockingSender struct {
	mu    sync.Mutex
	calls map[int64]int
}

func (b *blockingSender) Type() string { return "blocking" }
func (b *blockingSender) Send(ctx context.Context, ch *storage.NotificationChannel, _ *Payload) error {
	b.mu.Lock()
	b.calls[ch.ID]++
	b.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func newTestDispatcher(t *testing.T, st storage.Store) *Dispatcher {
	d := NewDispatcher(st, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	d.attemptTimeout = 100 * time.Millisecond
	d.baseBackoff = 10 * time.Millisecond
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		d.Shutdown(ctx)
	})
	return d
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// More channels than send slots: the caller must not block, queued sends get
// their full retry budget once they have a slot, and every send leaves a
// history row.
func TestDispatcherQueuedSendsAreNotDropped(t *testing.T) {
	st := &historyStore{}
	for i := 1; i <= maxConcurrentSends+1; i++ {
		st.channels = append(st.channels, &storage.NotificationChannel{ID: int64(i), Type: "blocking", Enabled: true})
	}
	d := newTestDispatcher(t, st)
	snd := &blockingSender{calls: map[int64]int{}}
	d.RegisterSender(snd)

	t0 := time.Now()
	d.NotifyWithPayload(context.Background(), &Payload{EventType: "incident.created"})
	if blocked := time.Since(t0); blocked > 50*time.Millisecond {
		t.Fatalf("NotifyWithPayload blocked the caller for %v", blocked)
	}

	waitFor(t, 10*time.Second, func() bool { return len(st.snapshot()) == len(st.channels) })
	snd.mu.Lock()
	defer snd.mu.Unlock()
	for _, ch := range st.channels {
		if got := snd.calls[ch.ID]; got != maxRetries {
			t.Errorf("channel %d: %d send attempts, want %d", ch.ID, got, maxRetries)
		}
	}
}

// Senders share a transport, so repeated sends reuse one keep-alive connection.
func TestWebhookReusesConnections(t *testing.T) {
	var newConns atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			newConns.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()

	settings, _ := json.Marshal(WebhookSettings{URL: srv.URL})
	ch := &storage.NotificationChannel{ID: 1, Type: "webhook", Settings: settings}
	ws := &WebhookSender{AllowPrivate: true}
	runtime.GC()
	g0 := runtime.NumGoroutine()
	const n = 50
	for i := 0; i < n; i++ {
		if err := ws.Send(context.Background(), ch, &Payload{EventType: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	g1 := runtime.NumGoroutine()
	t.Logf("%d sends -> %d new TCP connections; goroutines %d -> %d", n, newConns.Load(), g0, g1)
	if newConns.Load() > 1 {
		t.Errorf("%d sends opened %d connections (no reuse); %d goroutines left alive", n, newConns.Load(), g1-g0)
	}
	if g1-g0 > 10 {
		t.Errorf("goroutines grew from %d to %d after %d sends", g0, g1, n)
	}
}

// fakeSMTPNoStartTLS is an SMTP server that does not advertise STARTTLS, as
// when an on-path attacker strips it. It sends any received message body to got.
func fakeSMTPNoStartTLS(t *testing.T) (host string, port int, got chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got = make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		write := func(s string) { conn.Write([]byte(s + "\r\n")) }
		write("220 mitm ESMTP")
		var body strings.Builder
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				break
			}
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				write("250 mitm") // STARTTLS stripped
			case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
				write("250 OK")
			case line == "DATA":
				write("354 go")
				for {
					l, _ := r.ReadString('\n')
					if strings.TrimSpace(l) == "." {
						break
					}
					body.WriteString(l)
				}
				write("250 OK")
			case line == "QUIT":
				write("221 bye")
				got <- body.String()
				return
			default:
				write("250 OK")
			}
		}
		got <- body.String()
	}()
	a := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", a.Port, got
}

func TestEmailSTARTTLSRequired(t *testing.T) {
	host, port, got := fakeSMTPNoStartTLS(t)
	s := EmailSettings{Host: host, Port: port, From: "a@example.com", To: []string{"b@example.com"}, TLSMode: TLSModeSTARTTLSRequired}
	err := sendSTARTTLS(context.Background(), fmt.Sprintf("%s:%d", host, port), host, s, s.To, []byte("Subject: secret\r\n\r\nincident details token=XYZ\r\n"))
	if err == nil {
		select {
		case b := <-got:
			t.Fatalf("starttls mode delivered mail in plaintext to a server without STARTTLS: %q", b)
		case <-time.After(2 * time.Second):
			t.Fatal("starttls mode returned nil error without TLS")
		}
	}
}

func TestSubscriberSTARTTLSRequired(t *testing.T) {
	host, port, got := fakeSMTPNoStartTLS(t)
	n := NewSubscriberNotifier(nil, config.SMTPConfig{Host: host, Port: port, From: "a@example.com", TLSMode: TLSModeSTARTTLSRequired}, "", slog.New(slog.NewTextHandler(io.Discard, nil)), true)
	err := n.sendSTARTTLS(context.Background(), fmt.Sprintf("%s:%d", host, port), host, []string{"b@example.com"}, []byte("Subject: x\r\n\r\nunsubscribe token=XYZ\r\n"))
	if err == nil {
		select {
		case b := <-got:
			t.Fatalf("subscriber starttls mode delivered mail in plaintext: %q", b)
		case <-time.After(2 * time.Second):
			t.Fatal("subscriber starttls returned nil error without TLS")
		}
	}
}

// legacyTLSModes are the tls_mode values that existed in v1.3.0 and fell
// through to smtp.SendMail: empty (the API default) and "starttls" (the
// channel form's default). Both must keep upgrading opportunistically, so a
// relay without STARTTLS still receives the mail.
var legacyTLSModes = []string{"", "starttls"}

// The relay is on loopback and the sender is built without
// allow_private_targets, because SMTP relays are operator-configured.
func TestEmailLegacyTLSModeSendsToPlainLocalRelay(t *testing.T) {
	for _, mode := range legacyTLSModes {
		t.Run("mode="+mode, func(t *testing.T) {
			host, port, got := fakeSMTPNoStartTLS(t)
			settings, _ := json.Marshal(EmailSettings{Host: host, Port: port, From: "a@example.com", To: []string{"b@example.com"}, TLSMode: mode})
			ch := &storage.NotificationChannel{ID: 1, Name: "mail", Type: "email", Settings: settings}
			if err := (&EmailSender{AllowPrivate: true}).Send(context.Background(), ch, &Payload{EventType: "test"}); err != nil {
				t.Fatalf("tls_mode %q against a plain loopback relay: %v", mode, err)
			}
			select {
			case b := <-got:
				if !strings.Contains(b, "Asura") {
					t.Fatalf("unexpected body %q", b)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("mail not delivered")
			}
		})
	}
}

func TestSubscriberLegacyTLSModeSendsToPlainLocalRelay(t *testing.T) {
	for _, mode := range legacyTLSModes {
		t.Run("mode="+mode, func(t *testing.T) {
			host, port, got := fakeSMTPNoStartTLS(t)
			n := NewSubscriberNotifier(nil, config.SMTPConfig{Host: host, Port: port, From: "a@example.com", TLSMode: mode}, "", slog.New(slog.NewTextHandler(io.Discard, nil)), false)
			if err := n.sendSTARTTLS(context.Background(), fmt.Sprintf("%s:%d", host, port), host, []string{"b@example.com"}, []byte("Subject: x\r\n\r\nhello\r\n")); err != nil {
				t.Fatalf("subscription SMTP with tls_mode %q against a plain loopback relay: %v", mode, err)
			}
			select {
			case b := <-got:
				if !strings.Contains(b, "hello") {
					t.Fatalf("unexpected body %q", b)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("mail not delivered")
			}
		})
	}
}

// A server that accepts TCP but never answers must not block a send forever:
// the context deadline has to bound the whole SMTP conversation.
func TestSMTPStalledServerHonoursDeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var mu sync.Mutex
	var conns []net.Conn
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	}()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	addr := ln.Addr().String()
	s := EmailSettings{Host: "127.0.0.1", From: "a@example.com", To: []string{"b@example.com"}}
	for name, send := range map[string]func(context.Context) error{
		"plain":    func(ctx context.Context) error { return sendPlain(ctx, addr, "127.0.0.1", s, s.To, []byte("x")) },
		"starttls": func(ctx context.Context) error { return sendSTARTTLS(ctx, addr, "127.0.0.1", s, s.To, []byte("x")) },
		"smtps":    func(ctx context.Context) error { return sendSMTPS(ctx, addr, "127.0.0.1", s, s.To, []byte("x")) },
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		done := make(chan error, 1)
		go func() { done <- send(ctx) }()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("%s: expected an error from a stalled server", name)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s: send to a stalled SMTP server did not honour the context deadline", name)
		}
		cancel()
	}
}

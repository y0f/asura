package notifier

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/storage"
)

type senderFunc func(context.Context, *storage.NotificationChannel, *Payload) error

func (senderFunc) Type() string { return "fixture" }
func (f senderFunc) Send(ctx context.Context, ch *storage.NotificationChannel, p *Payload) error {
	return f(ctx, ch, p)
}

func fixtureChannel() *storage.NotificationChannel {
	return &storage.NotificationChannel{ID: 1, Type: "fixture", Enabled: true, Settings: []byte(`{"token":"original"}`)}
}

func TestDispatcherQueueBoundAndCancellationAccounting(t *testing.T) {
	st := &historyStore{channels: []*storage.NotificationChannel{fixtureChannel()}}
	d := newTestDispatcher(t, st)
	d.attemptTimeout = time.Minute
	started := make(chan struct{}, maxConcurrentSends)
	d.RegisterSender(senderFunc(func(ctx context.Context, _ *storage.NotificationChannel, _ *Payload) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}))
	base := runtime.NumGoroutine()
	for range maxConcurrentSends {
		if err := d.NotifyWithPayload(context.Background(), &Payload{EventType: "test"}); err != nil {
			t.Fatal(err)
		}
		<-started
	}
	for range maxQueuedSends {
		if err := d.NotifyWithPayload(context.Background(), &Payload{EventType: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := d.NotifyWithPayload(ctx, &Payload{EventType: "test"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("admission ignored request: %v", err)
	}
	if err := d.NotifyWithPayload(context.Background(), &Payload{EventType: "test"}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("full queue: %v", err)
	}
	if got := runtime.NumGoroutine() - base; got > maxConcurrentSends+4 {
		t.Fatalf("queued jobs created goroutines: %d", got)
	}
	if len(d.queue.jobs) != maxQueuedSends {
		t.Fatal("queue exceeded its bound")
	}
	stopCtx, stop := context.WithCancel(context.Background())
	stop()
	if err := d.Shutdown(stopCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown cancellation: %v", err)
	}
	stats := d.Stats()
	if stats.Accepted != 110 || stats.Failed != 110 || stats.Rejected != 2 || stats.Delivered != 0 {
		t.Fatalf("lost delivery accounting: %+v", stats)
	}
	if got := len(st.snapshot()); got != 112 {
		t.Fatalf("history count %d, want every accepted/rejected job", got)
	}
	for _, h := range st.snapshot() {
		if h.Status != "failed" || h.Error == "" {
			t.Fatalf("missing failure: %+v", h)
		}
	}
}

func TestDispatcherGracefulDrainAndImmutableSnapshots(t *testing.T) {
	ch := fixtureChannel()
	st := &historyStore{channels: []*storage.NotificationChannel{ch}}
	d := newTestDispatcher(t, st)
	start, release := make(chan struct{}), make(chan struct{})
	checked := make(chan string, 2)
	d.RegisterSender(senderFunc(func(ctx context.Context, got *storage.NotificationChannel, p *Payload) error {
		close(start)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		if string(got.Settings) != `{"token":"original"}` || p.Incident.Status != "open" || p.Monitor.Tags[0] != "original" || *p.Monitor.GroupID != 7 {
			checked <- "snapshot mutated"
		}
		if strings.Contains(string(p.Monitor.Settings), "private-token") || p.Monitor.ProxyURL != "" || p.Change.OldBody != "" || p.Change.NewBody != "" {
			checked <- "snapshot retains unnecessary secrets/body"
		}
		return nil
	}))
	group := int64(7)
	p := &Payload{EventType: "incident.created", Incident: &storage.Incident{Status: "open"},
		Monitor: &storage.Monitor{Tags: []string{"original"}, GroupID: &group, ProxyURL: "http://secret", Settings: []byte(`{"bearer_token":"private-token"}`)},
		Change:  &storage.ContentChange{OldBody: "large old body", NewBody: "large new body"}}
	if err := d.NotifyWithPayload(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	<-start
	ch.Settings[2] = 'X'
	p.Incident.Status = "resolved"
	p.Monitor.Tags[0] = "changed"
	group = 9
	done := make(chan error, 1)
	go func() { done <- d.Shutdown(context.Background()) }()
	select {
	case <-done:
		t.Fatal("shutdown returned before active send completed")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-checked:
		t.Fatal(msg)
	default:
	}
	if d.Stats().Delivered != 1 || len(st.snapshot()) != 1 {
		t.Fatal("graceful shutdown lost accepted job")
	}
	if err := d.NotifyWithPayload(context.Background(), p); !errors.Is(err, ErrClosed) {
		t.Fatalf("accepted after shutdown: %v", err)
	}
}

func TestDispatcherCancellationInterruptsBackoffAndAccountsPanic(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(map[bool]string{false: "backoff", true: "panic"}[panics], func(t *testing.T) {
			st := &historyStore{channels: []*storage.NotificationChannel{fixtureChannel()}}
			d := newTestDispatcher(t, st)
			d.baseBackoff = time.Hour
			called := make(chan struct{}, 1)
			d.RegisterSender(senderFunc(func(context.Context, *storage.NotificationChannel, *Payload) error {
				called <- struct{}{}
				if panics {
					panic("secret-token")
				}
				return errors.New("retry me")
			}))
			if err := d.NotifyWithPayload(context.Background(), &Payload{EventType: "test"}); err != nil {
				t.Fatal(err)
			}
			<-called
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			d.Shutdown(ctx)
			rows := st.snapshot()
			if len(rows) != 1 || rows[0].Status != "failed" || strings.Contains(rows[0].Error, "secret-token") {
				t.Fatalf("failure accounting: %+v", rows)
			}
		})
	}
}

type lookupBlockingStore struct {
	historyStore
	entered, canceled, release chan struct{}
}

type historyBlockingStore struct {
	historyStore
	calls atomic.Int64
}

func (s *historyBlockingStore) InsertNotificationHistory(ctx context.Context, _ *storage.NotificationHistory) error {
	s.calls.Add(1)
	<-ctx.Done()
	return ctx.Err()
}

func TestCancelledFanoutSharesOneHistoryBudget(t *testing.T) {
	st := &historyBlockingStore{}
	for range 1000 {
		st.channels = append(st.channels, fixtureChannel())
	}
	d := newTestDispatcher(t, st)
	d.RegisterSender(senderFunc(func(context.Context, *storage.NotificationChannel, *Payload) error { return nil }))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- d.NotifyWithPayload(ctx, &Payload{EventType: "test"}) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("fanout cancellation: %v", err)
		}
	case <-time.After(historyTimeout + time.Second):
		t.Fatal("rejected batch restarted history timeout per channel")
	}
	if st.calls.Load() != 1 || d.Stats().Rejected != 1000 {
		t.Fatalf("failure accounting not bounded: writes=%d stats=%+v", st.calls.Load(), d.Stats())
	}
	d.Shutdown(context.Background())
}

func (s *lookupBlockingStore) ListNotificationChannels(ctx context.Context) ([]*storage.NotificationChannel, error) {
	close(s.entered)
	<-ctx.Done()
	close(s.canceled)
	<-s.release
	return nil, ctx.Err()
}

func TestDispatcherShutdownJoinsAdmissionAndIsConcurrentSafe(t *testing.T) {
	st := &lookupBlockingStore{entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	d := newTestDispatcher(t, st)
	admitted := make(chan error, 1)
	go func() { admitted <- d.NotifyWithPayload(context.Background(), &Payload{EventType: "test"}) }()
	<-st.entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stopped := make(chan error, 4)
	for range 4 {
		go func() { stopped <- d.Shutdown(ctx) }()
	}
	<-st.canceled
	select {
	case <-stopped:
		t.Fatal("shutdown returned while lookup still used store")
	default:
	}
	close(st.release)
	if err := <-admitted; !errors.Is(err, context.Canceled) {
		t.Fatalf("lookup not cancelled: %v", err)
	}
	for range 4 {
		<-stopped
	}
	if err := d.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDispatcherSendTestHonorsRequestAndAttemptTimeout(t *testing.T) {
	for _, requestCancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "attempt", true: "request"}[requestCancel], func(t *testing.T) {
			st := &historyStore{}
			d := newTestDispatcher(t, st)
			d.attemptTimeout = 30 * time.Millisecond
			started, stopped := make(chan struct{}), make(chan struct{})
			d.RegisterSender(senderFunc(func(ctx context.Context, _ *storage.NotificationChannel, _ *Payload) error {
				close(started)
				<-ctx.Done()
				close(stopped)
				return ctx.Err()
			}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- d.SendTest(ctx, fixtureChannel(), nil) }()
			<-started
			if requestCancel {
				cancel()
			}
			if err := <-done; err == nil {
				t.Fatal("test send ignored cancellation/deadline")
			}
			<-stopped
			d.Shutdown(context.Background())
			if d.Stats().Failed != 1 {
				t.Fatalf("test send missing failure: %+v", d.Stats())
			}
		})
	}
}

func TestDispatcherRegistrationDuringAdmissionAndShutdown(t *testing.T) {
	st := &historyStore{channels: []*storage.NotificationChannel{fixtureChannel()}}
	d := newTestDispatcher(t, st)
	sender := senderFunc(func(context.Context, *storage.NotificationChannel, *Payload) error { return nil })
	d.RegisterSender(sender)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				d.RegisterSender(sender)
				_ = d.NotifyWithPayload(context.Background(), &Payload{EventType: "test"})
			}
		})
	}
	wg.Go(func() { _ = d.Shutdown(context.Background()) })
	wg.Wait()
	d.Shutdown(context.Background())
	stats := d.Stats()
	if stats.Accepted != stats.Delivered+stats.Failed {
		t.Fatalf("unfinished jobs after join: %+v", stats)
	}
}

type subscriberBatchStore struct {
	storage.Store
	pages atomic.Int64
}

func (*subscriberBatchStore) GetStatusPageIDsForMonitor(context.Context, int64) ([]int64, error) {
	return []int64{1}, nil
}
func (*subscriberBatchStore) GetStatusPage(context.Context, int64) (*storage.StatusPage, error) {
	return &storage.StatusPage{ID: 1, Enabled: true, PasswordHash: "must-not-retain"}, nil
}
func (s *subscriberBatchStore) ListConfirmedSubscribersAfter(_ context.Context, _ int64, after int64, limit int) ([]*storage.StatusPageSubscriber, error) {
	s.pages.Add(1)
	if limit != maxQueuedSends {
		return nil, errors.New("unbounded recipient read")
	}
	var result []*storage.StatusPageSubscriber
	for id := after + 1; id <= 205 && len(result) < limit; id++ {
		result = append(result, &storage.StatusPageSubscriber{ID: id, StatusPageID: 1, Type: "invalid"})
	}
	return result, nil
}

func TestSubscriberFanoutIsPagedAndJoined(t *testing.T) {
	st := &subscriberBatchStore{}
	n := NewSubscriberNotifier(st, config.SMTPConfig{}, "", slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	defer n.Shutdown(context.Background())
	if err := n.NotifyForMonitor(context.Background(), 1, "incident.created", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := n.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.pages.Load() != 3 || n.Stats().Accepted != 205 || n.Stats().Failed != 205 {
		t.Fatalf("fanout lost recipients: pages=%d counts=%+v", st.pages.Load(), n.Stats())
	}
	if err := n.QueueConfirmationEmail(context.Background(), &storage.StatusPageSubscriber{}, &storage.StatusPage{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("confirmation accepted after close: %v", err)
	}
}

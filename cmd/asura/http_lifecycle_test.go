package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPForcedCloseJoinsHandlerBeforeStoreClose(t *testing.T) {
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var storeClosed, lateAccess atomic.Bool
	tracker := &requestTracker{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(canceled)
		<-release
		// Model a handler that needs to finish database cleanup after its
		// request was canceled. Returning from Wait must cover this access.
		lateAccess.Store(storeClosed.Load())
	})}
	srv := httptest.NewServer(tracker)
	defer srv.Close()
	var releaseOnce sync.Once
	releaseHandler := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseHandler()
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		resp, err := srv.Client().Get(srv.URL)
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-entered
	tracker.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := srv.Config.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown should wait for active request: %v", err)
	}
	if err := srv.Config.Close(); err != nil {
		t.Fatal(err)
	}
	<-canceled
	joined := make(chan struct{})
	go func() { tracker.Wait(); storeClosed.Store(true); close(joined) }()
	select {
	case <-joined:
		t.Fatal("handler still uses store after forced-close join")
	default:
	}
	w := httptest.NewRecorder()
	tracker.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("accepted request after shutdown: %d", w.Code)
	}
	releaseHandler()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("handler join never finished")
	}
	<-clientDone
	if lateAccess.Load() {
		t.Fatal("store closed while handler was still running")
	}
}

func TestHTTPRequestAdmissionAndConcurrentWait(t *testing.T) {
	for range 25 {
		var calls atomic.Int64
		tracker := &requestTracker{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) })}
		start := make(chan struct{})
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				<-start
				for range 20 {
					tracker.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
				}
			})
		}
		for range 3 {
			wg.Go(func() { <-start; tracker.Wait() })
		}
		close(start)
		wg.Wait()
		tracker.Wait()
		before := calls.Load()
		w := httptest.NewRecorder()
		tracker.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if calls.Load() != before || w.Code != http.StatusServiceUnavailable {
			t.Fatal("handler admitted after concurrent shutdown")
		}
	}
}

package main

import (
	"net/http"
	"sync"
)

// requestTracker joins handlers even after http.Server.Close cancels their
// sockets. Stopping admission under the same mutex as Add makes Wait safe when
// the final request races with shutdown and the counter was previously zero.
type requestTracker struct {
	handler http.Handler
	mu      sync.Mutex
	closing bool
	active  sync.WaitGroup
}

func (t *requestTracker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	if t.closing {
		t.mu.Unlock()
		http.Error(w, "Server is shutting down", http.StatusServiceUnavailable)
		return
	}
	t.active.Add(1)
	t.mu.Unlock()
	defer t.active.Done()
	t.handler.ServeHTTP(w, r)
}

func (t *requestTracker) Stop() {
	t.mu.Lock()
	t.closing = true
	t.mu.Unlock()
}

func (t *requestTracker) Wait() {
	t.Stop()
	t.active.Wait()
}

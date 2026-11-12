package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/y0f/asura/v2/internal/checker"
	"github.com/y0f/asura/v2/internal/model"
)

type workChecker struct {
	check func(context.Context, *model.Monitor) (*checker.Result, error)
}

func (workChecker) Type() string { return "http" }
func (c workChecker) Check(ctx context.Context, m *model.Monitor) (*checker.Result, error) {
	return c.check(ctx, m)
}

func TestAgentWorkBoundsWaitingGoroutines(t *testing.T) {
	jobs := make([]job, 1000)
	for i := range jobs {
		jobs[i] = job{ID: int64(i + 1), ConfigRevision: 1, Type: "http", Interval: 3600, Timeout: 30}
	}
	_, srv := newFakeAsura(t, jobs)
	entered := make(chan struct{}, 1000)
	registry := checker.NewRegistry()
	registry.Register(workChecker{check: func(ctx context.Context, _ *model.Monitor) (*checker.Result, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	a := newAgent(srv.Client(), srv.URL, "fixture", registry, testLogger)
	a.concurrency = 2
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	before := runtime.NumGoroutine()
	go func() { defer close(done); a.runCycle(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("cancelled workers did not stop")
		}
	}()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("checks did not start")
		}
	}
	// Let the scheduler finish launching work while both active checks block.
	// The margin accommodates HTTP transport goroutines, not one per job.
	time.Sleep(100 * time.Millisecond)
	if extra := runtime.NumGoroutine() - before; extra > 32 {
		t.Errorf("1000 jobs with concurrency 2 created %d goroutines", extra)
	}
	select {
	case <-entered:
		t.Error("more checks started than the concurrency limit")
	default:
	}
}

func TestAgentWorkRetriesFailedResultsAndNewRevisions(t *testing.T) {
	var revision atomic.Int64
	revision.Store(1)
	var posts, checks atomic.Int64
	var mu sync.Mutex
	var received []result
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]any{"jobs": []job{{ID: 1, ConfigRevision: revision.Load(), Type: "http", Interval: 3600, Timeout: 5}}})
			return
		}
		var batch []result
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		received = append(received, batch...)
		mu.Unlock()
		if posts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]int{"accepted": len(batch)})
	}))
	defer srv.Close()
	registry := checker.NewRegistry()
	registry.Register(workChecker{check: func(context.Context, *model.Monitor) (*checker.Result, error) {
		checks.Add(1)
		return &checker.Result{Status: "up"}, nil
	}})
	a := newAgent(srv.Client(), srv.URL, "fixture", registry, testLogger)
	for range 3 {
		a.runCycle(context.Background())
	}
	if checks.Load() != 2 || posts.Load() != 2 {
		t.Fatalf("failed submission did not run a fresh check next poll: checks=%d posts=%d", checks.Load(), posts.Load())
	}
	revision.Store(2)
	a.runCycle(context.Background())
	if checks.Load() != 3 || posts.Load() != 3 {
		t.Fatalf("hourly monitor did not run immediately after revision change: checks=%d posts=%d", checks.Load(), posts.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if !received[1].CheckedAt.After(received[0].CheckedAt) || received[2].ConfigRevision != 2 {
		t.Fatalf("retry replayed stale observation or revision: %+v", received)
	}
}

func TestAgentWorkBoundsResultBatches(t *testing.T) {
	// Escaping expands these 2 MiB raw values to 4 MiB on the wire. The entire
	// cycle exceeds the server limit; each transmitted batch must fit 8 MiB.
	message := strings.Repeat("\"", 2<<20)
	var mu sync.Mutex
	var ids []int64
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, (8<<20)+1))
		if err != nil || len(body) > 8<<20 {
			t.Errorf("result request exceeds the 8 MiB batch limit: bytes=%d err=%v", len(body), err)
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		var batch []result
		if err := json.Unmarshal(body, &batch); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests++
		for _, r := range batch {
			ids = append(ids, r.MonitorID)
		}
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]int{"accepted": len(batch)})
	}))
	defer srv.Close()
	results := []result{
		{MonitorID: 1, ConfigRevision: 1, Status: "up", Message: message},
		{MonitorID: 2, ConfigRevision: 1, Status: "up", Message: message},
		{MonitorID: 3, ConfigRevision: 1, Status: "up", Message: message},
	}
	if err := postResults(context.Background(), srv.Client(), srv.URL, "fixture", results); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 3 || len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Fatalf("results lost, duplicated or reordered: requests=%d ids=%v", requests, ids)
	}
}

func TestAgentWorkRejectsInvalidPollingOptions(t *testing.T) {
	for _, args := range []string{"-interval=0", "-interval=-1s", "-concurrency=0", "-concurrency=-1"} {
		t.Run(args, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperAgentMain$")
			cmd.Env = append(os.Environ(), "ASURA_TEST_AGENT_MAIN=1", "ASURA_AGENT_TOKEN=fixture", "ASURA_TEST_AGENT_ARGS=-server http://127.0.0.1:1 "+args)
			out, err := cmd.CombinedOutput()
			if err == nil || ctx.Err() != nil || !strings.Contains(string(out), "interval and concurrency must be positive") || strings.Contains(string(out), "panic:") {
				t.Fatalf("invalid options were not rejected cleanly: %v %s", err, out)
			}
		})
	}
}

func TestAgentStreamsWithBackpressureAndKeepsAcknowledgedSchedules(t *testing.T) {
	jobs := make([]job, 100)
	for i := range jobs {
		jobs[i] = job{ID: int64(i + 1), ConfigRevision: 1, Type: "http", Interval: 3600, Timeout: 5}
	}
	var checks atomic.Int64
	var mu sync.Mutex
	byID := make(map[int64]int)
	var acknowledged, rejected []result
	blocked := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]any{"jobs": jobs})
			return
		}
		var batch []result
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch requests.Add(1) {
		case 1:
			mu.Lock()
			acknowledged = batch
			mu.Unlock()
		case 2:
			mu.Lock()
			rejected = batch
			mu.Unlock()
			close(blocked)
			select {
			case <-release:
			case <-r.Context().Done():
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]int{"accepted": len(batch)})
	}))
	defer srv.Close()
	registry := checker.NewRegistry()
	message := strings.Repeat("x", 1<<20)
	registry.Register(workChecker{check: func(_ context.Context, m *model.Monitor) (*checker.Result, error) {
		checks.Add(1)
		mu.Lock()
		byID[m.ID]++
		mu.Unlock()
		return &checker.Result{Status: "up", Message: message}, nil
	}})
	a := newAgent(srv.Client(), srv.URL, "fixture", registry, testLogger)
	a.concurrency = 2
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.runCycle(ctx) }()
	defer func() {
		unblock()
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("streaming senders did not stop")
		}
	}()
	select {
	case <-blocked:
	case <-time.After(10 * time.Second):
		t.Fatal("results were not streamed while jobs remained")
	}
	// Two seven-result batches, the overflow result, two channel entries and
	// two workers are enough. A slow server must stop the rest of the queue.
	if n := checks.Load(); n > 19 {
		t.Errorf("blocked result submission allowed %d/100 checks to accumulate", n)
	}
	unblock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("failed submission did not cancel and join producers")
	}
	mu.Lock()
	if len(acknowledged) != 7 || len(rejected) != 7 || len(a.lastRun) != len(acknowledged) {
		t.Errorf("incorrect retained schedules: accepted=%d rejected=%d schedules=%d", len(acknowledged), len(rejected), len(a.lastRun))
	}
	for _, r := range acknowledged {
		if a.lastRun[r.MonitorID].started != r.CheckedAt {
			t.Error("schedule did not use the check's actual start time")
		}
	}
	mu.Unlock()
	a.runCycle(context.Background())
	mu.Lock()
	defer mu.Unlock()
	for _, r := range acknowledged {
		if byID[r.MonitorID] != 1 {
			t.Errorf("acknowledged monitor %d was checked again before its interval", r.MonitorID)
		}
	}
	for _, r := range rejected {
		if byID[r.MonitorID] != 2 {
			t.Errorf("unacknowledged monitor %d was not checked fresh", r.MonitorID)
		}
	}
	if len(a.lastRun) != len(jobs) {
		t.Errorf("fresh retry did not restore all schedules: %d", len(a.lastRun))
	}
}

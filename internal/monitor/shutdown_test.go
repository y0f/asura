package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/y0f/asura/internal/checker"
	"github.com/y0f/asura/internal/storage"
)

type shutdownChecker struct {
	started, canceled, release chan struct{}
}

func (*shutdownChecker) Type() string { return "shutdown-fixture" }

func (c *shutdownChecker) Check(ctx context.Context, _ *storage.Monitor) (*checker.Result, error) {
	if c.started != nil {
		close(c.started)
		<-ctx.Done()
		close(c.canceled)
		<-c.release
	}
	return &checker.Result{Status: "up"}, nil
}

func TestWorkerResultSendCancellation(t *testing.T) {
	for _, kind := range []string{"unknown", "shutdown-fixture"} {
		t.Run(kind, func(t *testing.T) {
			registry := checker.NewRegistry()
			c := &shutdownChecker{make(chan struct{}), make(chan struct{}), make(chan struct{})}
			registry.Register(c)
			results := make(chan WorkerResult)
			pool := NewPool(1, registry, nil, results, discardLogger())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() {
				pool.executeJob(ctx, Job{Monitor: &storage.Monitor{Type: kind, Timeout: 30}})
				close(done)
			}()
			if kind == c.Type() {
				<-c.started
			}
			cancel()
			if kind == c.Type() {
				<-c.canceled
				close(c.release)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("canceled worker requires a result consumer to exit")
				<-results
				<-done
			}
		})
	}
}

func TestPoolRunJoinsCheckCleanup(t *testing.T) {
	c := &shutdownChecker{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	registry := checker.NewRegistry()
	registry.Register(c)
	jobs := make(chan Job, 1)
	jobs <- Job{Monitor: &storage.Monitor{Type: c.Type(), Timeout: 30}}
	pool := NewPool(1, registry, jobs, make(chan WorkerResult), discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { pool.Run(ctx); close(done) }()
	<-c.started
	cancel()
	<-c.canceled
	select {
	case <-done:
		t.Error("pool returned while a checker still owned resources")
	case <-time.After(30 * time.Millisecond):
	}
	close(c.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pool did not finish after checker cleanup")
	}
}

type shutdownSchedulerStore struct {
	storage.Store
	started, canceled, release chan struct{}
}

func (s *shutdownSchedulerStore) GetAllEnabledMonitors(ctx context.Context) ([]*storage.Monitor, error) {
	close(s.started)
	<-ctx.Done()
	close(s.canceled)
	<-s.release
	return nil, ctx.Err()
}

func TestPipelineRunJoinsScheduler(t *testing.T) {
	s := &shutdownSchedulerStore{started: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	p := NewPipeline(s, checker.NewRegistry(), nil, 1, false, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	<-s.started
	cancel()
	<-s.canceled
	select {
	case <-done:
		t.Error("pipeline returned before scheduler shutdown")
	case <-time.After(30 * time.Millisecond):
	}
	close(s.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pipeline did not finish after scheduler cleanup")
	}
}

func (s *shutdownSchedulerStore) ListPendingIncidentIDs(context.Context, int64, int) ([]int64, error) {
	return nil, nil
}

func (s *shutdownSchedulerStore) ListSLAMonitorIDs(context.Context, int64, int) ([]int64, error) {
	return nil, nil
}

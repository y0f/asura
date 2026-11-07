package notifier

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrQueueFull = errors.New("notification queue is full")
	ErrClosed    = errors.New("notification delivery is shutting down")
)

const (
	maxConcurrentSends = 10
	maxQueuedSends     = 100
	admissionTimeout   = 100 * time.Millisecond
	historyTimeout     = 2 * time.Second
)

// DeliveryStats counts completed attempts at delivery, not individual retries.
// Rejected jobs were never accepted; Failed jobs include shutdown cancellation.
type DeliveryStats struct {
	Accepted, Delivered, Failed, Rejected uint64
}

type deliveryJob struct {
	run  func(context.Context) error
	done func(error)
}

// deliveryQueue owns all asynchronous delivery. Callers hold an admission lease
// across lookups, enqueueing and rejection accounting so Shutdown cannot close
// the queue or return while a caller still uses the store.
type deliveryQueue struct {
	mu        sync.Mutex
	started   bool
	closing   bool
	callers   sync.WaitGroup
	workers   sync.WaitGroup
	jobs      chan deliveryJob
	stopping  chan struct{}
	done      chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc
	accepted  atomic.Uint64
	delivered atomic.Uint64
	failed    atomic.Uint64
	rejected  atomic.Uint64
}

func newDeliveryQueue() *deliveryQueue {
	ctx, cancel := context.WithCancel(context.Background())
	return &deliveryQueue{
		jobs: make(chan deliveryJob, maxQueuedSends), stopping: make(chan struct{}),
		done: make(chan struct{}), ctx: ctx, cancel: cancel,
	}
}

func (q *deliveryQueue) begin(ctx context.Context) (context.Context, func(), error) {
	q.mu.Lock()
	if q.closing {
		q.mu.Unlock()
		q.rejected.Add(1)
		return nil, nil, ErrClosed
	}
	q.callers.Add(1)
	if !q.started {
		q.started = true
		for range maxConcurrentSends {
			q.workers.Go(q.work)
		}
	}
	q.mu.Unlock()
	linked, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(q.ctx, cancel)
	return linked, func() { stop(); cancel(); q.callers.Done() }, nil
}

// offer waits briefly for space, without creating a waiting goroutine. Its
// caller must hold a lease until any rejection has been accounted for.
func (q *deliveryQueue) offer(ctx context.Context, job deliveryJob) error {
	if err := ctx.Err(); err != nil {
		q.rejected.Add(1)
		return err
	}
	timer := time.NewTimer(admissionTimeout)
	defer timer.Stop()
	select {
	case q.jobs <- job:
		q.accepted.Add(1)
		return nil
	case <-q.stopping:
		q.rejected.Add(1)
		return ErrClosed
	case <-ctx.Done():
		q.rejected.Add(1)
		return ctx.Err()
	case <-timer.C:
		q.rejected.Add(1)
		return ErrQueueFull
	}
}

func (q *deliveryQueue) work() {
	for job := range q.jobs {
		err := runDelivery(q.ctx, job.run)
		if err != nil {
			q.failed.Add(1)
		} else {
			q.delivered.Add(1)
		}
		if job.done != nil {
			job.done(err)
		}
	}
}

func runDelivery(ctx context.Context, run func(context.Context) error) (err error) {
	defer func() {
		if recover() != nil {
			// Panic values may contain sender credentials or response bodies.
			err = errors.New("notification sender panicked")
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	return run(ctx)
}

// shutdown stops admission, drains accepted jobs within ctx, then cancels and
// joins any remaining work. It always joins before returning, including when
// ctx expires. Senders must honor cancellation; completion accounting uses its
// own short deadline while the owner keeps the database open.
func (q *deliveryQueue) shutdown(ctx context.Context) error {
	q.mu.Lock()
	if !q.closing {
		q.closing = true
		close(q.stopping)
		go func() {
			q.callers.Wait()
			close(q.jobs)
			q.workers.Wait()
			q.cancel()
			close(q.done)
		}()
	}
	q.mu.Unlock()
	select {
	case <-q.done:
		return nil
	case <-ctx.Done():
		q.cancel()
		<-q.done
		return fmt.Errorf("notification shutdown: %w", ctx.Err())
	}
}

func (q *deliveryQueue) stats() DeliveryStats {
	return DeliveryStats{q.accepted.Load(), q.delivered.Load(), q.failed.Load(), q.rejected.Load()}
}

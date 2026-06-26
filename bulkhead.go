package ambatukam

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"
)

// BulkheadPolicy caps the number of in-flight requests reaching the next
// policy (typically the HTTP transport / downstream). Additional requests
// either wait on a bounded queue (up to cfg.MaxQueue) or fail fast with
// ErrBulkheadFull when the queue is full or queueing is disabled.
//
// Implementation notes:
//   - The semaphore `sem` is a buffered channel of struct{} with capacity
//     cfg.MaxConcurrent. Sending into it is non-blocking when a slot is
//     free; it would block once the bulkhead is full.
//   - The `queue` channel is used as a non-blocking admission gate: the
//     Execute path briefly inserts and removes a marker to test whether
//     there is queue capacity left, then waits on the semaphore for up to
//     cfg.QueueTimeout (or 1s as a safety net when timeout is 0).
//   - inFlight and denied counters are atomic for observability via
//     InFlight() / Denied().
type BulkheadPolicy struct {
	cfg    BulkheadConfig
	logger *slog.Logger

	sem   chan struct{} // semaphore: capacity = MaxConcurrent
	queue chan struct{} // queue slot tracker: capacity = MaxQueue

	inFlight atomic.Uint32
	denied   atomic.Uint64
}

// NewBulkhead constructs a BulkheadPolicy. Zero-valued cfg.MaxConcurrent
// defaults to runtime.NumCPU()*2 (floored at 1) so the policy is always
// usable out of the box.
func NewBulkhead(cfg BulkheadConfig) *BulkheadPolicy {
	if cfg.MaxConcurrent == 0 {
		cfg.MaxConcurrent = uint32(runtime.NumCPU() * 2)
		if cfg.MaxConcurrent < 1 {
			cfg.MaxConcurrent = 1
		}
	}
	return &BulkheadPolicy{
		cfg:    cfg,
		logger: slog.Default(),
		sem:    make(chan struct{}, cfg.MaxConcurrent),
		queue:  make(chan struct{}, cfg.MaxQueue),
	}
}

// WithLogger sets a non-nil logger on the policy.
func (b *BulkheadPolicy) WithLogger(l *slog.Logger) *BulkheadPolicy {
	if l != nil {
		b.logger = l
	}
	return b
}

// Execute runs a request through the bulkhead. Admitted requests call next.
// Denied requests return (nil, ErrBulkheadFull) (wrapped with "queue timeout"
// when the wait timed out).
func (b *BulkheadPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	// Try non-blocking acquire of semaphore.
	select {
	case b.sem <- struct{}{}:
		// acquired
		b.inFlight.Add(1)
		defer func() {
			<-b.sem
			b.inFlight.Add(^uint32(0)) // decrement
		}()
		return next(ctx, req)
	default:
		// semaphore full
	}

	// No queue? Fail fast.
	if b.cfg.MaxQueue == 0 {
		b.denied.Add(1)
		return nil, fmt.Errorf("%w: max_concurrent=%d, max_queue=%d",
			ErrBulkheadFull, b.cfg.MaxConcurrent, b.cfg.MaxQueue)
	}

	// Try to take a queue slot (non-blocking).
	select {
	case b.queue <- struct{}{}:
		// got queue slot; release it immediately (we'll wait on sem)
		<-b.queue
	default:
		b.denied.Add(1)
		return nil, fmt.Errorf("%w: max_concurrent=%d, max_queue=%d",
			ErrBulkheadFull, b.cfg.MaxConcurrent, b.cfg.MaxQueue)
	}

	// Wait for semaphore with QueueTimeout (or 1 second default if 0+queue but no timeout).
	timeout := b.cfg.QueueTimeout
	if timeout == 0 {
		// queued but no timeout — wait up to 1s as a safety net to avoid unbounded waits
		timeout = time.Second
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case b.sem <- struct{}{}:
		b.inFlight.Add(1)
		defer func() {
			<-b.sem
			b.inFlight.Add(^uint32(0))
		}()
		return next(ctx, req)
	case <-timer.C:
		b.denied.Add(1)
		return nil, fmt.Errorf("%w: max_concurrent=%d, max_queue=%d, queue_timeout=%v",
			ErrBulkheadFull, b.cfg.MaxConcurrent, b.cfg.MaxQueue, b.cfg.QueueTimeout)
	case <-ctx.Done():
		b.denied.Add(1)
		return nil, ctx.Err()
	}
}

// InFlight returns the current count of in-flight requests (observability).
func (b *BulkheadPolicy) InFlight() uint32 { return b.inFlight.Load() }

// Denied returns the total count of denied requests since startup (observability).
func (b *BulkheadPolicy) Denied() uint64 { return b.denied.Load() }

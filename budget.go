package ambatukam

import (
	"sync"
	"sync/atomic"
	"time"
)

type RetryBudget struct {
	budget     float64
	window     time.Duration
	total      atomic.Int64
	retries    atomic.Int64
	windowStart atomic.Int64
	mu         sync.Mutex
}

func NewRetryBudget(budget float64, window time.Duration) *RetryBudget {
	if budget <= 0 || budget > 1 {
		budget = 0.1
	}
	if window <= 0 {
		window = 10 * time.Second
	}
	return &RetryBudget{
		budget:      budget,
		window:      window,
		windowStart: atomic.Int64(time.Now().UnixNano()),
	}
}

func (rb *RetryBudget) Allow() bool {
	now := time.Now().UnixNano()
	start := rb.windowStart.Load()

	if now-start > int64(rb.window) {
		rb.mu.Lock()
		if rb.windowStart.Load() == start {
			rb.total.Store(0)
			rb.retries.Store(0)
			rb.windowStart.Store(now)
		}
		rb.mu.Unlock()
		start = rb.windowStart.Load()
	}

	rb.total.Add(1)
	total := rb.total.Load()
	retries := rb.retries.Load()

	if total <= 0 {
		return true
	}

	ratio := float64(retries) / float64(total)
	if ratio >= rb.budget {
		return false
	}

	rb.retries.Add(1)
	return true
}

func (rb *RetryBudget) Stats() (total, retries int64) {
	return rb.total.Load(), rb.retries.Load()
}

package ambatukam

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type RateLimitPolicy struct {
	cfg    RateLimitConfig
	logger *slog.Logger

	mu         sync.Mutex
	tokens     float64
	lastRefill time.Time

	disabled bool
	closedAll bool
}

func NewRateLimit(cfg RateLimitConfig) *RateLimitPolicy {
	if cfg.Burst == 0 {
		cfg.Burst = 1
	}
	if cfg.WaitTimeout < 0 {
		cfg.WaitTimeout = 0
	}
	return &RateLimitPolicy{
		cfg:       cfg,
		logger:    slog.Default(),
		tokens:    float64(cfg.Burst),
		disabled:  cfg.Rate == 0,
		closedAll: cfg.Rate < 0,
	}
}

func (r *RateLimitPolicy) WithLogger(l *slog.Logger) *RateLimitPolicy {
	if l != nil {
		r.logger = l
	}
	return r
}

func (r *RateLimitPolicy) refill(now time.Time) {
	if r.lastRefill.IsZero() {
		r.lastRefill = now
		return
	}
	elapsed := now.Sub(r.lastRefill).Seconds()
	if elapsed <= 0 {
		return
	}
	r.tokens += elapsed * r.cfg.Rate
	if r.tokens > float64(r.cfg.Burst) {
		r.tokens = float64(r.cfg.Burst)
	}
	r.lastRefill = now
}

func (r *RateLimitPolicy) tryAcquire(now time.Time) (bool, time.Duration) {
	r.refill(now)
	if r.tokens >= 1 {
		r.tokens -= 1
		return true, 0
	}
	deficit := 1 - r.tokens
	var wait time.Duration
	if r.cfg.Rate > 0 {
		wait = time.Duration(deficit / r.cfg.Rate * float64(time.Second))
	} else {
		wait = time.Hour * 24 * 365
	}
	return false, wait
}

func (r *RateLimitPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	if r.disabled {
		return next(ctx, req)
	}
	if r.closedAll {
		return nil, fmt.Errorf("%w: rate=%v, burst=%d, wait_timeout=%v",
			ErrRateLimited, r.cfg.Rate, r.cfg.Burst, r.cfg.WaitTimeout)
	}

	for {
		r.mu.Lock()
		now := time.Now()
		acquired, wait := r.tryAcquire(now)
		r.mu.Unlock()

		if acquired {
			return next(ctx, req)
		}

		if r.cfg.WaitTimeout == 0 {
			return nil, fmt.Errorf("%w: rate=%v, burst=%d, wait_timeout=%v",
				ErrRateLimited, r.cfg.Rate, r.cfg.Burst, r.cfg.WaitTimeout)
		}

		timeout := r.cfg.WaitTimeout
		if wait < timeout {
			timeout = wait
		}

		timer := time.NewTimer(timeout)
		select {
		case <-timer.C:
			continue
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
	}
}

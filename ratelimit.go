package ambatukam

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// RateLimitPolicy is a token-bucket rate limiter.
//
// State machine (per policy instance):
//   - The bucket holds `tokens` (a float) and is refilled continuously at
//     `cfg.Rate` tokens per second up to a cap of `cfg.Burst`.
//   - On every Execute call the bucket is refilled based on elapsed wall
//     time since `lastRefill`, then the policy either consumes one token
//     and forwards the request, or — depending on `cfg.WaitTimeout` —
//     either fails fast with ErrRateLimited or sleeps up to the time
//     required for one token to be available (capped by WaitTimeout).
//
// Concurrency:
//   - `mu` serialises refill + token consumption. It is never held across
//     `next(ctx, req)` — the mutex is released before the HTTP call runs,
//     so the rate limiter does not throttle concurrency, only throughput.
//   - There is an inherent best-effort race between releasing the mutex
//     and the next refill: a goroutine that wakes after a short deficit
//     can find another goroutine has already consumed the freshly
//     available token. The waiter then loops and tries again. The race
//     window is bounded by WaitTimeout. This is acceptable for typical
//     HTTP rate limiting; for strict admission control use a dedicated
//     library such as golang.org/x/time/rate.
type RateLimitPolicy struct {
	cfg    RateLimitConfig
	logger *slog.Logger

	mu         sync.Mutex
	tokens     float64
	lastRefill time.Time

	// disabled (Rate == 0): skip the limiter entirely; pass all requests.
	disabled bool
	// closedAll (Rate < 0): deny every request with ErrRateLimited.
	closedAll bool
}

// NewRateLimit constructs a RateLimitPolicy.
//
// Zero-valued fields are normalised:
//   - cfg.Burst == 0  → cfg.Burst = 1
//   - cfg.WaitTimeout < 0 → cfg.WaitTimeout = 0
//
// cfg.Rate semantics:
//   - cfg.Rate == 0  → disabled: every request passes through unchanged.
//   - cfg.Rate <  0  → closedAll: every request returns ErrRateLimited.
//   - cfg.Rate >  0  → normal token-bucket behaviour.
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
		// lastRefill left at zero value; first call to refill sets it.
	}
}

// WithLogger sets a non-nil logger on the policy.
func (r *RateLimitPolicy) WithLogger(l *slog.Logger) *RateLimitPolicy {
	if l != nil {
		r.logger = l
	}
	return r
}

// refill updates tokens based on elapsed time since lastRefill. Caller must
// hold mu.
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

// tryAcquire attempts to take one token. Returns (acquired, waitDuration).
// waitDuration is 0 if acquired=true; otherwise it is how long until one
// token is expected to be available.
//
// Caller must hold mu.
func (r *RateLimitPolicy) tryAcquire(now time.Time) (bool, time.Duration) {
	r.refill(now)
	if r.tokens >= 1 {
		r.tokens -= 1
		return true, 0
	}
	// tokens < 1: compute time until 1 token available.
	deficit := 1 - r.tokens
	var wait time.Duration
	if r.cfg.Rate > 0 {
		wait = time.Duration(deficit / r.cfg.Rate * float64(time.Second))
	} else {
		// Rate <= 0 means deny all; wait is effectively infinite, but
		// Execute short-circuits before reaching the timer when Rate<=0.
		wait = time.Hour * 24 * 365
	}
	return false, wait
}

// Execute runs a request through the rate limiter.
//
// Behaviour:
//   - disabled (Rate == 0): the request is forwarded unchanged.
//   - closedAll (Rate < 0): every request returns ErrRateLimited immediately.
//   - Bucket has a token: the token is consumed and the request is forwarded.
//   - Bucket is empty, WaitTimeout == 0: returns ErrRateLimited immediately.
//   - Bucket is empty, WaitTimeout > 0: sleeps up to WaitTimeout (capped by
//     the actual time-to-token), then retries. Honours ctx cancellation.
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

		// No token available.
		if r.cfg.WaitTimeout == 0 {
			return nil, fmt.Errorf("%w: rate=%v, burst=%d, wait_timeout=%v",
				ErrRateLimited, r.cfg.Rate, r.cfg.Burst, r.cfg.WaitTimeout)
		}

		// Wait up to WaitTimeout, but capped by the actual time-to-token.
		timeout := r.cfg.WaitTimeout
		if wait < timeout {
			timeout = wait
		}

		timer := time.NewTimer(timeout)
		select {
		case <-timer.C:
			// Loop and try again.
			continue
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
	}
}

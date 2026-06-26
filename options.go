package ambatukam

import (
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"time"
)

// Hooks are user callbacks invoked at key lifecycle points.
// All fields are optional. Any nil callback is skipped.
type Hooks struct {
	// BeforeRequest is called immediately before each HTTP attempt.
	// It can mutate the request (e.g. add Authorization header) or return
	// an error to abort the attempt. Returning an error short-circuits
	// the retry loop with that error.
	BeforeRequest func(req *http.Request) error

	// AfterResponse is called after each HTTP attempt completes (success or failure).
	// It is purely observational; its return value is ignored. Use it for logging,
	// metrics emission, or response inspection.
	AfterResponse func(req *http.Request, resp *http.Response, err error)

	// OnRetry is called between retry attempts, before the backoff sleep.
	// `attempt` is the 0-indexed attempt number that just completed (the next
	// attempt will be `attempt+1`). `nextDelay` is the planned sleep duration.
	OnRetry func(req *http.Request, attempt int, nextDelay time.Duration)

	// OnStateChange is called when a circuit breaker changes state.
	// `name` is the breaker's identifier (defaults to "default" if not set).
	// `from` and `to` are the old and new states (e.g. StateClosed → StateOpen).
	OnStateChange func(name string, from, to State)
}

type RetryConfig struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Multiplier     float64
	Jitter         float64
	Backoff        Backoff
	ShouldRetry    func(resp *http.Response, err error) bool
}

type CircuitConfig struct {
	FailureThreshold uint32
	OpenDuration     time.Duration
	HalfOpenMaxReqs  uint32
	ShouldTrip       func(resp *http.Response, err error) bool
}

type TimeoutConfig struct {
	Timeout time.Duration
}

type BulkheadConfig struct {
	MaxConcurrent uint32

	// MaxQueue is the number of requests that can wait when MaxConcurrent is reached.
	// Set to 0 to disable queueing (fail-fast on capacity).
	MaxQueue uint32

	// QueueTimeout is how long a queued request waits for a slot before
	// returning ErrBulkheadFull.
	//
	// Special behavior: if MaxQueue > 0 and QueueTimeout == 0, requests wait
	// up to 1 second as a safety net (avoiding unbounded waits). To get
	// fail-fast behavior, set MaxQueue = 0 instead.
	QueueTimeout time.Duration
}

// RateLimitConfig configures a token-bucket rate limiter policy.
//
// Rate is the steady-state token replenishment rate in tokens per second.
// A non-positive Rate denies every request immediately with ErrRateLimited.
//
// Burst is the bucket capacity — the maximum number of tokens that can be
// consumed back-to-back without replenishment. Zero is replaced with 1
// inside NewRateLimit.
//
// WaitTimeout controls behaviour when no token is available. Zero means
// fail fast (the request returns ErrRateLimited immediately). A positive
// value means the policy waits up to that duration for a token to become
// available; if the wait would exceed WaitTimeout the request is denied.
//
// Note: because the bucket is checked, then released, then re-checked
// between waits, this policy is best-effort under high concurrency. For
// strict admission control, consider a dedicated library. The race window
// is bounded by WaitTimeout: a goroutine that releases the mutex with a
// small remaining deficit and then sleeps WaitTimeout may find another
// goroutine has already consumed the freshly-refilled token, in which
// case it loops and tries again.
type RateLimitConfig struct {
	// Rate is the tokens per second. Special values:
	//   Rate == 0 (default): rate limiting is disabled — all requests pass through.
	//   Rate < 0: deny all requests (fail-closed).
	//   Rate > 0: normal token-bucket behavior.
	Rate float64
	// Burst is the bucket capacity (default 1 if 0).
	Burst uint32
	// WaitTimeout is how long to wait for a token when none is available.
	// 0 = fail fast; >0 = wait up to this long.
	WaitTimeout time.Duration
}

type Option func(*Client)

func WithRetry(cfg RetryConfig) Option {
	return func(c *Client) {
		p := NewRetry(cfg).WithLogger(c.logger)
		c.policies = append(c.policies, p)
	}
}
func WithCircuitBreaker(cfg CircuitConfig) Option {
	return func(c *Client) {
		cb := NewCircuitBreaker(cfg).WithLogger(c.logger)
		c.policies = append(c.policies, cb)
	}
}
func WithTimeout(cfg TimeoutConfig) Option {
	return func(c *Client) {
		c.policies = append(c.policies, NewTimeout(cfg))
	}
}
func WithBulkhead(cfg BulkheadConfig) Option {
	return func(c *Client) {
		c.policies = append(c.policies, NewBulkhead(cfg))
	}
}
func WithRateLimit(cfg RateLimitConfig) Option {
	return func(c *Client) {
		c.policies = append(c.policies, NewRateLimit(cfg).WithLogger(c.logger))
	}
}
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.hc = hc }
}
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) { c.logger = l }
}

// WithDebug enables verbose DEBUG-level logging via slog to stderr.
// All policy decisions (retries, circuit transitions, rate-limit waits) are logged.
//
// Equivalent to WithLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))).
func WithDebug() Option {
	return func(c *Client) {
		c.logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
}
func WithPolicy(p Policy) Option {
	return func(c *Client) { c.policies = append(c.policies, p) }
}

// WithRequestID enables automatic X-Request-ID generation/propagation.
// Pass an empty string to use the default header name.
func WithRequestID(header string) Option {
	return func(c *Client) {
		c.policies = append(c.policies, NewRequestIDPolicy().WithHeader(header))
	}
}

// WithRequestIDPolicy registers a custom-configured RequestIDPolicy.
// Use this when you need to set a custom header name or generator
// (see RequestIDPolicy.WithHeader, RequestIDPolicy.WithGenerator).
func WithRequestIDPolicy(p *RequestIDPolicy) Option {
	return func(c *Client) {
		c.policies = append(c.policies, p)
	}
}

// WithHooks installs user callbacks invoked at key request lifecycle points
// (before each attempt, after each response, between retries, and on circuit
// breaker state changes). Any nil field in Hooks is skipped.
//
// Hooks are propagated to registered retry and circuit breaker policies
// inside New(), so declaration order does not matter — WithHooks can be
// passed before or after WithRetry/WithCircuitBreaker with the same effect.
func WithHooks(h Hooks) Option {
	return func(c *Client) {
		c.hooks = h
	}
}

// DefaultConfig returns a sensible production-default *Client configuration.
// Use with New() to get a fully-configured Client:
//
//	client := ambatukam.New(ambatukam.DefaultConfig()...)
//
// Defaults:
//   - Retry: 3 attempts, exponential 100ms..5s, jitter 0.2
//   - Circuit: 5 failures to trip, 30s open duration, 1 half-open probe
//   - Timeout: 30s per attempt
//   - Bulkhead: runtime.NumCPU()*4 concurrent, no queue (fail fast)
//   - RateLimit: disabled (use WithRateLimit to enable)
//   - No request ID (use WithRequestID to enable)
//   - No hooks (use WithHooks to enable)
func DefaultConfig() []Option {
	return []Option{
		WithRetry(RetryConfig{
			MaxRetries:     3,
			InitialBackoff: 100 * time.Millisecond,
			MaxBackoff:     5 * time.Second,
			Multiplier:     2.0,
			Jitter:         0.2,
		}),
		WithCircuitBreaker(CircuitConfig{
			FailureThreshold: 5,
			OpenDuration:     30 * time.Second,
			HalfOpenMaxReqs:  1,
		}),
		WithTimeout(TimeoutConfig{Timeout: 30 * time.Second}),
		WithBulkhead(BulkheadConfig{
			MaxConcurrent: uint32(runtime.NumCPU() * 4),
			MaxQueue:      0,
		}),
	}
}

// NewDefaultClient is a convenience constructor: New(DefaultConfig()...).
func NewDefaultClient() *Client {
	return New(DefaultConfig()...)
}

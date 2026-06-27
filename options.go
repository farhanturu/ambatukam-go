package ambatukam

import (
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"time"
)

type Hooks struct {
	BeforeRequest func(ctx *http.Request) error
	AfterResponse func(req *http.Request, resp *http.Response, err error)
	OnRetry       func(req *http.Request, attempt int, nextDelay time.Duration)
	OnStateChange func(name string, from, to State)
	OnFallback    func(req *http.Request, err error)
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
	MaxQueue      uint32
	QueueTimeout  time.Duration
}

type RateLimitConfig struct {
	Rate        float64
	Burst       uint32
	WaitTimeout time.Duration
}

type FallbackConfig struct {
	Handler func(req *http.Request, err error) (*http.Response, error)
}

type SingleflightConfig struct {
	Enabled bool
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
func WithDebug() Option {
	return func(c *Client) {
		c.logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
}
func WithPolicy(p Policy) Option {
	return func(c *Client) { c.policies = append(c.policies, p) }
}
func WithRequestID(header string) Option {
	return func(c *Client) {
		c.policies = append(c.policies, NewRequestIDPolicy().WithHeader(header))
	}
}
func WithRequestIDPolicy(p *RequestIDPolicy) Option {
	return func(c *Client) {
		c.policies = append(c.policies, p)
	}
}
func WithHooks(h Hooks) Option {
	return func(c *Client) {
		c.hooks = h
	}
}
func WithFallback(cfg FallbackConfig) Option {
	return func(c *Client) {
		c.policies = append(c.policies, NewFallback(cfg))
	}
}
func WithSingleflight() Option {
	return func(c *Client) {
		c.policies = append(c.policies, NewSingleflight())
	}
}
func WithMetrics(r MetricsRecorder) Option {
	return func(c *Client) {
		c.metrics = r
	}
}
func WithTimeoutMap(rules map[string]time.Duration) Option {
	return func(c *Client) {
		c.policies = append(c.policies, NewTimeoutMap(rules))
	}
}
func WithCustomLogger(l Logger) Option {
	return func(c *Client) {
		c.customLogger = l
	}
}

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

func NewDefaultClient() *Client {
	return New(DefaultConfig()...)
}

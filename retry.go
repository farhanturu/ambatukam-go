package ambatukam

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// RetryPolicy retries failed requests with configurable backoff.
// The request body is buffered once before any attempt so each retry can
// re-read it safely (safe POST retry).
type RetryPolicy struct {
	cfg     RetryConfig
	backoff Backoff
	logger  *slog.Logger
	hooks   Hooks
}

// DefaultRetryConfig returns the retry configuration used when none is provided.
// Callers can extend these defaults programmatically.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     5 * time.Second,
		Multiplier:     2.0,
		Jitter:         0.2,
	}
}

// applyRetryDefaults fills zero-valued cfg fields with the package defaults.
// MaxRetries is floored at 0 (negatives become 0); Jitter is clamped to [0,1].
// Backoff is initialised to an ExponentialBackoff from the merged initial/max/multiplier
// settings when no Backoff was supplied by the caller.
func applyRetryDefaults(cfg RetryConfig) RetryConfig {
	def := DefaultRetryConfig()
	if cfg.InitialBackoff == 0 {
		cfg.InitialBackoff = def.InitialBackoff
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = def.MaxBackoff
	}
	if cfg.Multiplier == 0 {
		cfg.Multiplier = def.Multiplier
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.Jitter < 0 {
		cfg.Jitter = 0
	}
	if cfg.Jitter > 1 {
		cfg.Jitter = 1
	}
	if cfg.Backoff == nil {
		cfg.Backoff = ExponentialBackoff(cfg.InitialBackoff, cfg.MaxBackoff, cfg.Multiplier)
	}
	return cfg
}

// NewRetry constructs a RetryPolicy. Zero-valued cfg fields are filled with
// the package defaults via applyRetryDefaults. Defaults: MaxRetries floored
// at 0, backoff defaults to exponential 100ms..5s with multiplier 2,
// jitter clamped to [0,1].
func NewRetry(cfg RetryConfig) *RetryPolicy {
	cfg = applyRetryDefaults(cfg)
	return &RetryPolicy{cfg: cfg, backoff: cfg.Backoff, logger: slog.Default()}
}

// WithLogger sets a non-nil logger on the policy.
func (r *RetryPolicy) WithLogger(l *slog.Logger) *RetryPolicy {
	if l != nil {
		r.logger = l
	}
	return r
}

// WithHooks installs user callbacks fired during the retry lifecycle. Each
// non-nil callback is invoked at the appropriate point: BeforeRequest before
// each attempt (errors abort the loop), AfterResponse after each attempt,
// OnRetry between attempts before the backoff sleep.
func (r *RetryPolicy) WithHooks(h Hooks) *RetryPolicy {
	r.hooks = h
	return r
}

// Execute runs the request through next, retrying transient failures per config.
func (r *RetryPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	// CRITICAL: buffer body ONCE before any attempt so each retry can re-read it.
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("retry: read body: %w", err)
		}
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(bodyBytes)), nil
		}
		req.ContentLength = int64(len(bodyBytes))
	}

	var lastResp *http.Response
	var lastErr error
	attempts := r.cfg.MaxRetries + 1
	for attempt := 0; attempt < attempts; attempt++ {
		// reset body for each retry
		if bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		// BeforeRequest hook: runs before each attempt. Returning an error
		// aborts the loop and propagates that error to the caller; the
		// request never goes out so AfterResponse is intentionally skipped.
		if r.hooks.BeforeRequest != nil {
			if hookErr := r.hooks.BeforeRequest(req); hookErr != nil {
				return nil, hookErr
			}
		}

		resp, err := next(ctx, req)
		lastResp, lastErr = resp, err

		// AfterResponse hook: observational only, runs after every attempt
		// (whether we end up retrying or returning the final response).
		if r.hooks.AfterResponse != nil {
			r.hooks.AfterResponse(req, resp, err)
		}

		// decide whether to retry
		should := r.shouldRetry(req, resp, err)
		if !should {
			return resp, err
		}
		if attempt == attempts-1 {
			// last attempt exhausted
			break
		}

		delay := r.nextDelay(resp, attempt)

		// OnRetry hook: called between attempts, before the backoff sleep.
		if r.hooks.OnRetry != nil {
			r.hooks.OnRetry(req, attempt, delay)
		}

		r.logger.Debug("retry: backing off",
			slog.Int("attempt", attempt+1),
			slog.Int("max", attempts),
			slog.Duration("delay", delay),
		)

		// respect ctx cancel during sleep
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}

		if resp != nil {
			resp.Body.Close()
		}
	}

	return nil, &RequestError{
		Method:   req.Method,
		URL:      req.URL.String(),
		Status:   statusFromResp(lastResp),
		Attempts: attempts,
		Err:      fmt.Errorf("%w: %w", ErrMaxRetries, lastErrOrStatus(lastResp, lastErr)),
	}
}

// nextDelay returns the delay to wait before the next retry. On 429/503
// responses with a Retry-After header, the header value is used (capped at
// MaxBackoff). Otherwise, the configured backoff strategy is consulted.
func (r *RetryPolicy) nextDelay(resp *http.Response, attempt int) time.Duration {
	delay := r.backoff.NextDelay(attempt)
	if resp != nil && (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) {
		if ra := parseRetryAfter(resp); ra > 0 {
			delay = ra
			if r.cfg.MaxBackoff > 0 && delay > r.cfg.MaxBackoff {
				delay = r.cfg.MaxBackoff
			}
		}
	}
	return delay
}

// parseRetryAfter returns the delay suggested by the Retry-After header.
// Supports both delta-seconds (integer) and HTTP-date formats.
// Returns 0 if the header is missing, malformed, or in the past.
// The returned duration is capped at one year as a defensive sanity bound,
// and integer overflow when converting delta-seconds to time.Duration is
// guarded against.
func parseRetryAfter(resp *http.Response) time.Duration {
	const maxDelay = 365 * 24 * time.Hour
	if resp == nil {
		return 0
	}
	h := resp.Header.Get("Retry-After")
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		if secs < 0 {
			return 0
		}
		// Guard against int64 overflow when multiplying secs * time.Second.
		// time.Second is 1e9 ns; max int64 is ~9.22e18 ns => cap secs accordingly.
		const nsPerSec = int64(time.Second)
		const maxSecs = int64(^uint64(0)>>1) / nsPerSec
		if int64(secs) > maxSecs {
			return maxDelay
		}
		d := time.Duration(secs) * time.Second
		if d > maxDelay {
			return maxDelay
		}
		return d
	}
	if t, err := http.ParseTime(h); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		if d > maxDelay {
			return maxDelay
		}
		return d
	}
	return 0
}

// statusFromResp returns the response status code, or 0 when resp is nil.
func statusFromResp(r *http.Response) int {
	if r == nil {
		return 0
	}
	return r.StatusCode
}

// lastErrOrStatus returns the underlying error when present; otherwise a
// synthetic error describing the last response status; otherwise a generic
// "unknown" error. Used when surfacing a RequestError after retries exhaust.
func lastErrOrStatus(r *http.Response, err error) error {
	if err != nil {
		return err
	}
	if r != nil {
		return fmt.Errorf("status %d", r.StatusCode)
	}
	return errors.New("unknown")
}

// shouldRetry decides whether to retry based on the configured predicate
// or a default heuristic (network errors and 5xx for idempotent methods only).
func (r *RetryPolicy) shouldRetry(req *http.Request, resp *http.Response, err error) bool {
	// Permanent errors never retry, regardless of method or status.
	var perm *PermanentError
	if errors.As(err, &perm) {
		return false
	}
	// Defensive nil-guard: a nil req can occur if caller passes the result of
	// http.NewRequest("", url, body) which returns nil,err. Without this guard,
	// req.Method access below would panic.
	if req == nil {
		return err != nil // retry on network errors, not on missing request
	}
	if r.cfg.ShouldRetry != nil {
		return r.cfg.ShouldRetry(resp, err)
	}
	// default: retry network errors, 5xx, and 429/503 on idempotent methods only
	if !isIdempotent(req.Method) {
		return false
	}
	if err != nil {
		return true
	}
	if resp != nil && (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode >= 500) {
		return true
	}
	return false
}

// isIdempotent returns true for HTTP methods that are safe to retry
// without changing server-visible state.
func isIdempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

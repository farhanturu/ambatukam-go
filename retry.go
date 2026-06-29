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

type RetryPolicy struct {
	cfg     RetryConfig
	backoff Backoff
	logger  *slog.Logger
	hooks   Hooks
	metrics MetricsRecorder
}

func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     5 * time.Second,
		Multiplier:     2.0,
		Jitter:         0.2,
	}
}

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
		cfg.Backoff = ExponentialBackoff(cfg.InitialBackoff, cfg.MaxBackoff, cfg.Multiplier, cfg.Jitter)
	}
	return cfg
}

func NewRetry(cfg RetryConfig) *RetryPolicy {
	cfg = applyRetryDefaults(cfg)
	return &RetryPolicy{cfg: cfg, backoff: cfg.Backoff, logger: slog.Default()}
}

func (r *RetryPolicy) WithLogger(l *slog.Logger) *RetryPolicy {
	if l != nil {
		r.logger = l
	}
	return r
}

func (r *RetryPolicy) WithHooks(h Hooks) *RetryPolicy {
	r.hooks = h
	return r
}

func (r *RetryPolicy) WithMetrics(m MetricsRecorder) *RetryPolicy {
	if m != nil {
		r.metrics = m
	}
	return r
}

func (r *RetryPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
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
		if bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		resp, err := next(ctx, req)
		lastResp, lastErr = resp, err

		should := r.shouldRetry(req, resp, err)
		if !should {
			return resp, err
		}
		if attempt == attempts-1 {
			break
		}

		delay := r.nextDelay(resp, attempt)

		if r.hooks.OnRetry != nil {
			r.hooks.OnRetry(req, attempt, delay)
		}
		if r.metrics != nil {
			r.metrics.RecordRetry(req.Method, req.URL.String(), attempt+1)
		}

		r.logger.Debug("retry: backing off",
			slog.Int("attempt", attempt+1),
			slog.Int("max", attempts),
			slog.Duration("delay", delay),
		)

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
		Policy:   "retry",
		Err:      fmt.Errorf("%w: %w", ErrMaxRetries, lastErrOrStatus(lastResp, lastErr)),
	}
}

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

func statusFromResp(r *http.Response) int {
	if r == nil {
		return 0
	}
	return r.StatusCode
}

func lastErrOrStatus(r *http.Response, err error) error {
	if err != nil {
		return err
	}
	if r != nil {
		return fmt.Errorf("status %d", r.StatusCode)
	}
	return errors.New("unknown")
}

func (r *RetryPolicy) shouldRetry(req *http.Request, resp *http.Response, err error) bool {
	var perm *PermanentError
	if errors.As(err, &perm) {
		return false
	}
	if req == nil {
		return err != nil
	}
	if r.cfg.ShouldRetry != nil {
		return r.cfg.ShouldRetry(resp, err)
	}
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

func isIdempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

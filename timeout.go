package ambatukam

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type TimeoutPolicy struct {
	timeout time.Duration
	metrics MetricsRecorder
}

func NewTimeout(cfg TimeoutConfig) *TimeoutPolicy {
	return &TimeoutPolicy{timeout: cfg.Timeout}
}

func (t *TimeoutPolicy) WithMetrics(m MetricsRecorder) *TimeoutPolicy {
	if m != nil {
		t.metrics = m
	}
	return t
}

func (t *TimeoutPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	if t.timeout <= 0 {
		return next(ctx, req)
	}
	childCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	req = req.WithContext(childCtx)
	resp, err := next(childCtx, req)
	if err != nil {
		if errors.Is(childCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			if t.metrics != nil {
				t.metrics.RecordTimeout(req.Method, req.URL.String())
			}
			return resp, fmt.Errorf("%w: %v", ErrTimeout, err)
		}
		return resp, err
	}
	return resp, nil
}

type TimeoutMapPolicy struct {
	rules   map[string]time.Duration
	metrics MetricsRecorder
}

func NewTimeoutMap(rules map[string]time.Duration) *TimeoutMapPolicy {
	return &TimeoutMapPolicy{rules: rules}
}

func (t *TimeoutMapPolicy) WithMetrics(m MetricsRecorder) *TimeoutMapPolicy {
	if m != nil {
		t.metrics = m
	}
	return t
}

func (t *TimeoutMapPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	timeout := t.matchURL(req.URL.Path)
	if timeout <= 0 {
		return next(ctx, req)
	}
	childCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req = req.WithContext(childCtx)
	resp, err := next(childCtx, req)
	if err != nil {
		if errors.Is(childCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			if t.metrics != nil {
				t.metrics.RecordTimeout(req.Method, req.URL.String())
			}
			return resp, fmt.Errorf("%w: %v", ErrTimeout, err)
		}
		return resp, err
	}
	return resp, nil
}

func (t *TimeoutMapPolicy) matchURL(path string) time.Duration {
	for pattern, timeout := range t.rules {
		if matchPattern(pattern, path) {
			return timeout
		}
	}
	return 0
}

func matchPattern(pattern, path string) bool {
	if pattern == path {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(path, prefix)
	}
	return false
}

package ambatukam

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

type FallbackPolicy struct {
	handler func(req *http.Request, err error) (*http.Response, error)
	hooks   Hooks
	metrics MetricsRecorder
}

func NewFallback(cfg FallbackConfig) *FallbackPolicy {
	return &FallbackPolicy{handler: cfg.Handler}
}

func (f *FallbackPolicy) WithHooks(h Hooks) *FallbackPolicy {
	f.hooks = h
	return f
}

func (f *FallbackPolicy) WithMetrics(m MetricsRecorder) *FallbackPolicy {
	if m != nil {
		f.metrics = m
	}
	return f
}

func (f *FallbackPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	resp, err := next(ctx, req)
	if err == nil {
		return resp, nil
	}
	if f.hooks.OnFallback != nil {
		f.hooks.OnFallback(req, err)
	}
	if f.metrics != nil {
		f.metrics.RecordFallback(req.Method, req.URL.String())
	}
	fallbackResp, fallbackErr := f.handler(req, err)
	if fallbackErr != nil {
		attempts := 1
		var reqErr *RequestError
		if errors.As(err, &reqErr) && reqErr.Attempts > 0 {
			attempts = reqErr.Attempts
		}
		return nil, &RequestError{
			Method:   req.Method,
			URL:      req.URL.String(),
			Policy:   "fallback",
			Attempts: attempts,
			Err:      fmt.Errorf("%w: %w", ErrFallback, fallbackErr),
		}
	}
	return fallbackResp, nil
}

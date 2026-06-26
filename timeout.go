package ambatukam

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// TimeoutPolicy applies a per-attempt deadline to the request passed to its
// `next` PolicyFunc. If the deadline fires before the downstream call returns,
// the error returned to the caller is wrapped with ErrTimeout so callers can
// distinguish a per-attempt timeout from other failures (e.g. parent context
// cancellation).
//
// A non-positive timeout disables the policy entirely.
type TimeoutPolicy struct {
	timeout time.Duration
}

// NewTimeout constructs a TimeoutPolicy from a TimeoutConfig.
func NewTimeout(cfg TimeoutConfig) *TimeoutPolicy {
	return &TimeoutPolicy{timeout: cfg.Timeout}
}

// Execute runs the downstream call under a child context with the configured
// timeout. If the timeout fires (child context's Err() is DeadlineExceeded)
// while the parent context is still healthy, the returned error is wrapped
// with ErrTimeout. If the parent context is already canceled (or its own
// deadline already fired), the underlying error is returned unchanged so the
// caller can distinguish between a per-attempt timeout and an outer cancel.
//
// The child context is attached to the request via req.WithContext so the
// downstream HTTP transport observes the per-attempt deadline (the transport
// reads from req.Context, not from any ctx argument the caller passes).
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
			return resp, fmt.Errorf("%w: %v", ErrTimeout, err)
		}
		return resp, err
	}
	return resp, nil
}

package ambatukam

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

type TimeoutPolicy struct {
	timeout time.Duration
}

func NewTimeout(cfg TimeoutConfig) *TimeoutPolicy {
	return &TimeoutPolicy{timeout: cfg.Timeout}
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
			return resp, fmt.Errorf("%w: %v", ErrTimeout, err)
		}
		return resp, err
	}
	return resp, nil
}

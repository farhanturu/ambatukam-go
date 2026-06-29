package ambatukam

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type RateLimitPolicy struct {
	cfg       RateLimitConfig
	logger    *slog.Logger
	metrics   MetricsRecorder
	tokens    chan struct{}
	stop      chan struct{}
	disabled  bool
	closedAll bool
}

func NewRateLimit(cfg RateLimitConfig) *RateLimitPolicy {
	if cfg.Burst == 0 {
		cfg.Burst = 1
	}
	if cfg.WaitTimeout < 0 {
		cfg.WaitTimeout = 0
	}
	r := &RateLimitPolicy{
		cfg:       cfg,
		logger:    slog.Default(),
		disabled:  cfg.Rate == 0,
		closedAll: cfg.Rate < 0,
	}
	if !r.disabled && !r.closedAll {
		r.tokens = make(chan struct{}, cfg.Burst)
		r.stop = make(chan struct{})
		for i := uint32(0); i < cfg.Burst; i++ {
			r.tokens <- struct{}{}
		}
		go r.refillLoop()
	}
	return r
}

func (r *RateLimitPolicy) WithLogger(l *slog.Logger) *RateLimitPolicy {
	if l != nil {
		r.logger = l
	}
	return r
}

func (r *RateLimitPolicy) WithMetrics(m MetricsRecorder) *RateLimitPolicy {
	if m != nil {
		r.metrics = m
	}
	return r
}

func (r *RateLimitPolicy) refillLoop() {
	interval := time.Duration(float64(time.Second) / r.cfg.Rate)
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			select {
			case r.tokens <- struct{}{}:
			default:
			}
		}
	}
}

func (r *RateLimitPolicy) deny(method, url string) {
	if r.metrics != nil {
		r.metrics.RecordRateLimitDenied(method, url)
	}
}

func (r *RateLimitPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	if r.disabled {
		return next(ctx, req)
	}
	if r.closedAll {
		r.deny(req.Method, req.URL.String())
		return nil, fmt.Errorf("%w: rate=%v, burst=%d, wait_timeout=%v",
			ErrRateLimited, r.cfg.Rate, r.cfg.Burst, r.cfg.WaitTimeout)
	}
	for {
		select {
		case <-r.tokens:
			return next(ctx, req)
		default:
		}
		if r.cfg.WaitTimeout == 0 {
			r.deny(req.Method, req.URL.String())
			return nil, fmt.Errorf("%w: rate=%v, burst=%d, wait_timeout=%v",
				ErrRateLimited, r.cfg.Rate, r.cfg.Burst, r.cfg.WaitTimeout)
		}
		select {
		case <-r.tokens:
			return next(ctx, req)
		case <-time.After(r.cfg.WaitTimeout):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (r *RateLimitPolicy) Close() {
	if r.stop != nil {
		close(r.stop)
	}
}

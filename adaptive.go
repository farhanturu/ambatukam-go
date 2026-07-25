package ambatukam

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"
)

type AdaptiveTimeoutConfig struct {
	Initial    time.Duration
	Percentile int
	Window     time.Duration
	MinSamples int
}

type AdaptiveTimeoutPolicy struct {
	cfg       AdaptiveTimeoutConfig
	durations []time.Duration
	mu        sync.RWMutex
	median    time.Duration
	metrics   MetricsRecorder
}

func NewAdaptiveTimeout(cfg AdaptiveTimeoutConfig) *AdaptiveTimeoutPolicy {
	if cfg.Initial == 0 {
		cfg.Initial = 5 * time.Second
	}
	if cfg.Percentile <= 0 || cfg.Percentile > 100 {
		cfg.Percentile = 99
	}
	if cfg.Window == 0 {
		cfg.Window = 5 * time.Minute
	}
	if cfg.MinSamples < 10 {
		cfg.MinSamples = 10
	}
	return &AdaptiveTimeoutPolicy{
		cfg:       cfg,
		durations: make([]time.Duration, 0, 256),
		median:    cfg.Initial,
	}
}

func (at *AdaptiveTimeoutPolicy) WithMetrics(m MetricsRecorder) *AdaptiveTimeoutPolicy {
	at.metrics = m
	return at
}

func (at *AdaptiveTimeoutPolicy) record(d time.Duration) {
	at.mu.Lock()
	at.durations = append(at.durations, d)
	if len(at.durations) > 1024 {
		at.durations = at.durations[len(at.durations)-512:]
	}
	if len(at.durations) >= at.cfg.MinSamples {
		sorted := make([]time.Duration, len(at.durations))
		copy(sorted, at.durations)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		idx := int(float64(len(sorted)-1) * float64(at.cfg.Percentile) / 100.0)
		at.median = sorted[idx]
	}
	at.mu.Unlock()
}

func (at *AdaptiveTimeoutPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	at.mu.RLock()
	timeout := at.median
	at.mu.RUnlock()

	if timeout <= 0 {
		timeout = at.cfg.Initial
	}

	childCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req = req.WithContext(childCtx)
	start := time.Now()
	resp, err := next(childCtx, req)
	elapsed := time.Since(start)

	at.record(elapsed)

	if err != nil {
		if childCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			if at.metrics != nil {
				at.metrics.RecordTimeout(req.Method, req.URL.String())
			}
			return resp, ErrTimeout
		}
		return resp, err
	}
	return resp, nil
}

func (at *AdaptiveTimeoutPolicy) CurrentTimeout() time.Duration {
	at.mu.RLock()
	defer at.mu.RUnlock()
	return at.median
}

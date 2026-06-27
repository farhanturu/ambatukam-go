package ambatukam

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"
)

type BulkheadPolicy struct {
	cfg     BulkheadConfig
	logger  *slog.Logger
	metrics MetricsRecorder

	sem    chan struct{}
	queue  chan struct{}
	closed atomic.Bool

	inFlight atomic.Uint32
	denied   atomic.Uint64
}

func NewBulkhead(cfg BulkheadConfig) *BulkheadPolicy {
	if cfg.MaxConcurrent == 0 {
		cfg.MaxConcurrent = uint32(runtime.NumCPU() * 2)
		if cfg.MaxConcurrent < 1 {
			cfg.MaxConcurrent = 1
		}
	}
	queueSize := int(cfg.MaxQueue)
	if queueSize < 1 {
		queueSize = 1
	}
	return &BulkheadPolicy{
		cfg:    cfg,
		logger: slog.Default(),
		sem:    make(chan struct{}, cfg.MaxConcurrent),
		queue:  make(chan struct{}, queueSize),
	}
}

func (b *BulkheadPolicy) WithLogger(l *slog.Logger) *BulkheadPolicy {
	if l != nil {
		b.logger = l
	}
	return b
}

func (b *BulkheadPolicy) WithMetrics(m MetricsRecorder) *BulkheadPolicy {
	if m != nil {
		b.metrics = m
	}
	return b
}

func (b *BulkheadPolicy) deny(method, url string) {
	b.denied.Add(1)
	if b.metrics != nil {
		b.metrics.RecordBulkheadDenied(method, url)
	}
}

func (b *BulkheadPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	select {
	case b.sem <- struct{}{}:
		b.inFlight.Add(1)
		defer func() {
			<-b.sem
			b.inFlight.Add(^uint32(0))
		}()
		return next(ctx, req)
	default:
	}

	if b.cfg.MaxQueue == 0 {
		b.deny(req.Method, req.URL.String())
		return nil, fmt.Errorf("%w: max_concurrent=%d, max_queue=%d",
			ErrBulkheadFull, b.cfg.MaxConcurrent, b.cfg.MaxQueue)
	}

	select {
	case b.queue <- struct{}{}:
		defer func() { <-b.queue }()
	default:
		b.deny(req.Method, req.URL.String())
		return nil, fmt.Errorf("%w: max_concurrent=%d, max_queue=%d",
			ErrBulkheadFull, b.cfg.MaxConcurrent, b.cfg.MaxQueue)
	}

	timeout := b.cfg.QueueTimeout
	if timeout == 0 {
		timeout = time.Second
	}

	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			b.deny(req.Method, req.URL.String())
			return nil, fmt.Errorf("%w: max_concurrent=%d, max_queue=%d, queue_timeout=%v",
				ErrBulkheadFull, b.cfg.MaxConcurrent, b.cfg.MaxQueue, b.cfg.QueueTimeout)
		}

		select {
		case b.sem <- struct{}{}:
			b.inFlight.Add(1)
			defer func() {
				<-b.sem
				b.inFlight.Add(^uint32(0))
			}()
			return next(ctx, req)
		case <-time.After(remaining):
			b.deny(req.Method, req.URL.String())
			return nil, fmt.Errorf("%w: max_concurrent=%d, max_queue=%d, queue_timeout=%v",
				ErrBulkheadFull, b.cfg.MaxConcurrent, b.cfg.MaxQueue, b.cfg.QueueTimeout)
		case <-ctx.Done():
			b.deny(req.Method, req.URL.String())
			return nil, ctx.Err()
		}
	}
}

func (b *BulkheadPolicy) InFlight() uint32 { return b.inFlight.Load() }

func (b *BulkheadPolicy) Denied() uint64 { return b.denied.Load() }

package ambatukam

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type bulkheadRequest struct {
	ctx      context.Context
	req      *http.Request
	next     PolicyFunc
	resultCh chan bulkheadResult
	dequeued chan struct{}
	priority int
}

type bulkheadResult struct {
	resp *http.Response
	err  error
}

type BulkheadPolicy struct {
	metrics  MetricsRecorder
	logger   *slog.Logger
	queue    chan *bulkheadRequest
	highQ    chan *bulkheadRequest
	quit     chan struct{}
	cfg      BulkheadConfig
	denied   atomic.Uint64
	inFlight atomic.Uint32
	closed   atomic.Bool
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
		queueSize = 0
	}
	b := &BulkheadPolicy{
		cfg:    cfg,
		logger: slog.Default(),
		queue:  make(chan *bulkheadRequest, queueSize),
		quit:   make(chan struct{}),
	}
	if cfg.Priority {
		b.highQ = make(chan *bulkheadRequest, queueSize)
	}
	var ready sync.WaitGroup
	ready.Add(int(cfg.MaxConcurrent))
	for i := uint32(0); i < cfg.MaxConcurrent; i++ {
		go func() {
			ready.Done()
			b.worker()
		}()
	}
	ready.Wait()
	return b
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

func (b *BulkheadPolicy) worker() {
	for {
		var wr *bulkheadRequest
		if b.highQ != nil {
			select {
			case <-b.quit:
				return
			case wr = <-b.highQ:
			default:
				select {
				case <-b.quit:
					return
				case wr = <-b.highQ:
				case wr = <-b.queue:
				}
			}
		} else {
			select {
			case <-b.quit:
				return
			case wr = <-b.queue:
			}
		}
		if wr == nil {
			continue
		}
		close(wr.dequeued)
		b.inFlight.Add(1)
		var resp *http.Response
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("ambatukam: bulkhead panic: %v", r)
				}
			}()
			resp, err = wr.next(wr.ctx, wr.req)
		}()
		b.inFlight.Add(^uint32(0))
		wr.resultCh <- bulkheadResult{resp: resp, err: err}
	}
}

type bulkheadPriorityKey struct{}

func WithPriority(ctx context.Context) context.Context {
	return context.WithValue(ctx, bulkheadPriorityKey{}, true)
}

func isPriority(ctx context.Context) bool {
	v, _ := ctx.Value(bulkheadPriorityKey{}).(bool)
	return v
}

func (b *BulkheadPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	if b.closed.Load() {
		return nil, fmt.Errorf("%w: bulkhead closed", ErrBulkheadFull)
	}
	resultCh := make(chan bulkheadResult, 1)
	wr := &bulkheadRequest{
		ctx:      ctx,
		req:      req,
		next:     next,
		resultCh: resultCh,
		dequeued: make(chan struct{}),
	}
	if b.cfg.MaxQueue == 0 {
		select {
		case b.queue <- wr:
		default:
			b.deny(req.Method, req.URL.String())
			return nil, fmt.Errorf("%w: max_concurrent=%d, max_queue=%d",
				ErrBulkheadFull, b.cfg.MaxConcurrent, b.cfg.MaxQueue)
		}
	} else {
		targetQ := b.queue
		if b.cfg.Priority && isPriority(ctx) && b.highQ != nil {
			targetQ = b.highQ
		}
		queueTimeout := b.cfg.QueueTimeout
		if queueTimeout == 0 {
			queueTimeout = time.Second
		}
		enqueueStart := time.Now()
		remaining := queueTimeout
		if remaining <= 0 {
			b.deny(req.Method, req.URL.String())
			return nil, fmt.Errorf("%w: max_concurrent=%d, max_queue=%d, queue_timeout=%v",
				ErrBulkheadFull, b.cfg.MaxConcurrent, b.cfg.MaxQueue, queueTimeout)
		}
		select {
		case targetQ <- wr:
		case <-time.After(remaining):
			b.deny(req.Method, req.URL.String())
			return nil, fmt.Errorf("%w: max_concurrent=%d, max_queue=%d, queue_timeout=%v",
				ErrBulkheadFull, b.cfg.MaxConcurrent, b.cfg.MaxQueue, queueTimeout)
		case <-ctx.Done():
			b.deny(req.Method, req.URL.String())
			return nil, ctx.Err()
		}
		// Use remaining time for dequeue wait, not a fresh timeout.
		remaining = queueTimeout - time.Since(enqueueStart)
		if remaining <= 0 {
			b.deny(req.Method, req.URL.String())
			return nil, fmt.Errorf("%w: max_concurrent=%d, max_queue=%d, queue_timeout=%v",
				ErrBulkheadFull, b.cfg.MaxConcurrent, b.cfg.MaxQueue, queueTimeout)
		}
		select {
		case <-wr.dequeued:
		case <-time.After(remaining):
			b.deny(req.Method, req.URL.String())
			return nil, fmt.Errorf("%w: max_concurrent=%d, max_queue=%d, queue_timeout=%v",
				ErrBulkheadFull, b.cfg.MaxConcurrent, b.cfg.MaxQueue, queueTimeout)
		case <-ctx.Done():
			b.deny(req.Method, req.URL.String())
			return nil, ctx.Err()
		}
	}
	select {
	case result := <-resultCh:
		return result.resp, result.err
	case <-ctx.Done():
		b.deny(req.Method, req.URL.String())
		return nil, ctx.Err()
	}
}

func (b *BulkheadPolicy) InFlight() uint32 { return b.inFlight.Load() }

func (b *BulkheadPolicy) Denied() uint64 { return b.denied.Load() }

func (b *BulkheadPolicy) Close() {
	if b.closed.CompareAndSwap(false, true) {
		close(b.quit)
	}
}

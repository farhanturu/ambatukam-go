package ambatukam

import (
	"sync"
	"sync/atomic"
	"time"
)

type ClientStats struct {
	CircuitState      State
	BulkheadInFlight  uint32
	BulkheadDenied    uint64
	RateLimitTokens   int
	RequestsTotal     uint64
	RequestsFailed    uint64
	RetriesTotal      uint64
	FallbacksTotal    uint64
	TimeoutsTotal     uint64
	CacheHits         uint64
	CacheMisses       uint64
	AvgResponseTimeNs int64
	Uptime            time.Duration
}

type statsRecorder struct {
	inner          MetricsRecorder
	requests       atomic.Uint64
	failed         atomic.Uint64
	retries        atomic.Uint64
	fallbacks      atomic.Uint64
	timeouts       atomic.Uint64
	cacheHits      atomic.Uint64
	cacheMisses    atomic.Uint64
	totalDuration  atomic.Int64
	started        time.Time
	mu             sync.RWMutex
	circuitState   State
	bulkheadDenied uint64
}

func newStatsRecorder(inner MetricsRecorder) *statsRecorder {
	return &statsRecorder{inner: inner, started: time.Now()}
}

func (s *statsRecorder) RecordRequest(method, url string, status int, duration time.Duration) {
	s.requests.Add(1)
	s.totalDuration.Add(int64(duration))
	if status == 0 || status >= 500 {
		s.failed.Add(1)
	}
	if s.inner != nil {
		s.inner.RecordRequest(method, url, status, duration)
	}
}

func (s *statsRecorder) RecordRetry(method, url string, attempt int) {
	s.retries.Add(1)
	if s.inner != nil {
		s.inner.RecordRetry(method, url, attempt)
	}
}

func (s *statsRecorder) RecordCircuitStateChange(name string, from, to State) {
	s.mu.Lock()
	s.circuitState = to
	s.mu.Unlock()
	if s.inner != nil {
		s.inner.RecordCircuitStateChange(name, from, to)
	}
}

func (s *statsRecorder) RecordBulkheadDenied(method, url string) {
	s.mu.Lock()
	s.bulkheadDenied++
	s.mu.Unlock()
	if s.inner != nil {
		s.inner.RecordBulkheadDenied(method, url)
	}
}

func (s *statsRecorder) RecordRateLimitDenied(method, url string) {
	if s.inner != nil {
		s.inner.RecordRateLimitDenied(method, url)
	}
}

func (s *statsRecorder) RecordFallback(method, url string) {
	s.fallbacks.Add(1)
	if s.inner != nil {
		s.inner.RecordFallback(method, url)
	}
}

func (s *statsRecorder) RecordTimeout(method, url string) {
	s.timeouts.Add(1)
	if s.inner != nil {
		s.inner.RecordTimeout(method, url)
	}
}

func (s *statsRecorder) snapshot(bulkheadInFlight uint32, rateTokens int) ClientStats {
	s.mu.RLock()
	cs := s.circuitState
	bd := s.bulkheadDenied
	s.mu.RUnlock()

	total := s.requests.Load()
	var avgNs int64
	if total > 0 {
		avgNs = s.totalDuration.Load() / int64(total)
	}

	return ClientStats{
		CircuitState:      cs,
		BulkheadInFlight:  bulkheadInFlight,
		BulkheadDenied:    bd,
		RateLimitTokens:   rateTokens,
		RequestsTotal:     total,
		RequestsFailed:    s.failed.Load(),
		RetriesTotal:      s.retries.Load(),
		FallbacksTotal:    s.fallbacks.Load(),
		TimeoutsTotal:     s.timeouts.Load(),
		CacheHits:         s.cacheHits.Load(),
		CacheMisses:       s.cacheMisses.Load(),
		AvgResponseTimeNs: avgNs,
		Uptime:            time.Since(s.started),
	}
}

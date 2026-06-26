package ambatukam

import (
	"time"
)

type MetricsRecorder interface {
	RecordRequest(method, url string, status int, duration time.Duration)
	RecordRetry(method, url string, attempt int)
	RecordCircuitStateChange(name string, from, to State)
	RecordBulkheadDenied(method, url string)
	RecordRateLimitDenied(method, url string)
	RecordFallback(method, url string)
	RecordTimeout(method, url string)
}

type noopMetricsRecorder struct{}

func (n *noopMetricsRecorder) RecordRequest(method, url string, status int, duration time.Duration) {}
func (n *noopMetricsRecorder) RecordRetry(method, url string, attempt int)                           {}
func (n *noopMetricsRecorder) RecordCircuitStateChange(name string, from, to State)                   {}
func (n *noopMetricsRecorder) RecordBulkheadDenied(method, url string)                                {}
func (n *noopMetricsRecorder) RecordRateLimitDenied(method, url string)                               {}
func (n *noopMetricsRecorder) RecordFallback(method, url string)                                      {}
func (n *noopMetricsRecorder) RecordTimeout(method, url string)                                       {}

func NewNoopMetricsRecorder() MetricsRecorder {
	return &noopMetricsRecorder{}
}

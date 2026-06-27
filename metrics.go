package ambatukam

import (
	"time"
)

type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

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

type CounterVec interface {
	WithLabelValues(lvs ...string) Counter
}

type GaugeVec interface {
	WithLabelValues(lvs ...string) Gauge
}

type HistogramVec interface {
	WithLabelValues(lvs ...string) Histogram
}

type Counter interface {
	Inc()
	Add(float64)
}

type Gauge interface {
	Set(float64)
	Inc()
	Dec()
}

type Histogram interface {
	Observe(float64)
}

type PrometheusConfig struct {
	RequestsTotal       CounterVec
	RetriesTotal        CounterVec
	CircuitState        GaugeVec
	RequestDuration     HistogramVec
	BulkheadDenied      CounterVec
	RateLimitDenied     CounterVec
	FallbacksTotal      CounterVec
	TimeoutsTotal       CounterVec
	CircuitTransitions  CounterVec
}

type PrometheusRecorder struct {
	cfg PrometheusConfig
}

func NewPrometheusRecorder(cfg PrometheusConfig) *PrometheusRecorder {
	return &PrometheusRecorder{cfg: cfg}
}

func (r *PrometheusRecorder) RecordRequest(method, url string, status int, duration time.Duration) {
	if r.cfg.RequestsTotal != nil {
		r.cfg.RequestsTotal.WithLabelValues(method, url, statusToString(status)).Inc()
	}
	if r.cfg.RequestDuration != nil {
		r.cfg.RequestDuration.WithLabelValues(method, url).Observe(duration.Seconds())
	}
}

func (r *PrometheusRecorder) RecordRetry(method, url string, attempt int) {
	if r.cfg.RetriesTotal != nil {
		r.cfg.RetriesTotal.WithLabelValues(method, url, intToString(attempt)).Inc()
	}
}

func (r *PrometheusRecorder) RecordCircuitStateChange(name string, from, to State) {
	if r.cfg.CircuitState != nil {
		r.cfg.CircuitState.WithLabelValues(name).Set(stateToFloat(to))
	}
	if r.cfg.CircuitTransitions != nil {
		r.cfg.CircuitTransitions.WithLabelValues(name, string(from), string(to)).Inc()
	}
}

func (r *PrometheusRecorder) RecordBulkheadDenied(method, url string) {
	if r.cfg.BulkheadDenied != nil {
		r.cfg.BulkheadDenied.WithLabelValues(method, url).Inc()
	}
}

func (r *PrometheusRecorder) RecordRateLimitDenied(method, url string) {
	if r.cfg.RateLimitDenied != nil {
		r.cfg.RateLimitDenied.WithLabelValues(method, url).Inc()
	}
}

func (r *PrometheusRecorder) RecordFallback(method, url string) {
	if r.cfg.FallbacksTotal != nil {
		r.cfg.FallbacksTotal.WithLabelValues(method, url).Inc()
	}
}

func (r *PrometheusRecorder) RecordTimeout(method, url string) {
	if r.cfg.TimeoutsTotal != nil {
		r.cfg.TimeoutsTotal.WithLabelValues(method, url).Inc()
	}
}

func statusToString(status int) string {
	if status == 0 {
		return "0"
	}
	return intToString(status)
}

func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + intToString(-n)
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func stateToFloat(s State) float64 {
	switch s {
	case StateClosed:
		return 0
	case StateOpen:
		return 1
	case StateHalfOpen:
		return 2
	default:
		return -1
	}
}

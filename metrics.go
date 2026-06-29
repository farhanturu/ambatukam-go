package ambatukam

import (
	"context"
	"log/slog"
	"strconv"
	"time"
)

type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

type customLoggerHandler struct {
	l     Logger
	group string
	attrs []slog.Attr
}

func (h *customLoggerHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *customLoggerHandler) Handle(_ context.Context, r slog.Record) error {
	args := make([]any, 0, len(h.attrs)*2+r.NumAttrs()*2)
	for _, a := range h.attrs {
		args = append(args, a.Key, a.Value.Any())
	}
	r.Attrs(func(a slog.Attr) bool {
		args = append(args, a.Key, a.Value.Any())
		return true
	})
	switch {
	case r.Level >= slog.LevelError:
		h.l.Error(r.Message, args...)
	case r.Level >= slog.LevelWarn:
		h.l.Warn(r.Message, args...)
	case r.Level >= slog.LevelInfo:
		h.l.Info(r.Message, args...)
	default:
		h.l.Debug(r.Message, args...)
	}
	return nil
}

func (h *customLoggerHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = make([]slog.Attr, len(h.attrs)+len(attrs))
	copy(clone.attrs, h.attrs)
	copy(clone.attrs[len(h.attrs):], attrs)
	return &clone
}

func (h *customLoggerHandler) WithGroup(name string) slog.Handler {
	clone := *h
	if h.group != "" {
		clone.group = h.group + "." + name
	} else {
		clone.group = name
	}
	return &clone
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

func (n *noopMetricsRecorder) RecordRequest(string, string, int, time.Duration) {}
func (n *noopMetricsRecorder) RecordRetry(string, string, int)                  {}
func (n *noopMetricsRecorder) RecordCircuitStateChange(string, State, State)    {}
func (n *noopMetricsRecorder) RecordBulkheadDenied(string, string)              {}
func (n *noopMetricsRecorder) RecordRateLimitDenied(string, string)             {}
func (n *noopMetricsRecorder) RecordFallback(string, string)                    {}
func (n *noopMetricsRecorder) RecordTimeout(string, string)                     {}

func NewNoopMetricsRecorder() MetricsRecorder { return &noopMetricsRecorder{} }

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
	RequestsTotal      CounterVec
	RetriesTotal       CounterVec
	CircuitState       GaugeVec
	RequestDuration    HistogramVec
	BulkheadDenied     CounterVec
	RateLimitDenied    CounterVec
	FallbacksTotal     CounterVec
	TimeoutsTotal      CounterVec
	CircuitTransitions CounterVec
}

type PrometheusRecorder struct {
	cfg PrometheusConfig
}

func NewPrometheusRecorder(cfg PrometheusConfig) *PrometheusRecorder {
	return &PrometheusRecorder{cfg: cfg}
}

func (r *PrometheusRecorder) RecordRequest(method, url string, status int, duration time.Duration) {
	if r.cfg.RequestsTotal != nil {
		r.cfg.RequestsTotal.WithLabelValues(method, url, strconv.Itoa(status)).Inc()
	}
	if r.cfg.RequestDuration != nil {
		r.cfg.RequestDuration.WithLabelValues(method, url).Observe(duration.Seconds())
	}
}

func (r *PrometheusRecorder) RecordRetry(method, url string, attempt int) {
	if r.cfg.RetriesTotal != nil {
		r.cfg.RetriesTotal.WithLabelValues(method, url, strconv.Itoa(attempt)).Inc()
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

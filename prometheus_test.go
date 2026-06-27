package ambatukam

import (
	"net/http"
	"testing"
	"time"
)

type mockCounter struct {
	value float64
}

func (c *mockCounter) Inc()          { c.value++ }
func (c *mockCounter) Add(v float64) { c.value += v }

type mockGauge struct {
	value float64
}

func (g *mockGauge) Set(v float64) { g.value = v }
func (g *mockGauge) Inc()          { g.value++ }
func (g *mockGauge) Dec()          { g.value-- }

type mockHistogram struct {
	values []float64
}

func (h *mockHistogram) Observe(v float64) { h.values = append(h.values, v) }

type mockCounterVec struct {
	counters map[string]*mockCounter
}

func newMockCounterVec() *mockCounterVec {
	return &mockCounterVec{counters: make(map[string]*mockCounter)}
}

func (v *mockCounterVec) WithLabelValues(lvs ...string) Counter {
	key := joinStrings(lvs)
	if c, ok := v.counters[key]; ok {
		return c
	}
	c := &mockCounter{}
	v.counters[key] = c
	return c
}

type mockGaugeVec struct {
	gauges map[string]*mockGauge
}

func newMockGaugeVec() *mockGaugeVec {
	return &mockGaugeVec{gauges: make(map[string]*mockGauge)}
}

func (v *mockGaugeVec) WithLabelValues(lvs ...string) Gauge {
	key := joinStrings(lvs)
	if g, ok := v.gauges[key]; ok {
		return g
	}
	g := &mockGauge{}
	v.gauges[key] = g
	return g
}

type mockHistogramVec struct {
	histograms map[string]*mockHistogram
}

func newMockHistogramVec() *mockHistogramVec {
	return &mockHistogramVec{histograms: make(map[string]*mockHistogram)}
}

func (v *mockHistogramVec) WithLabelValues(lvs ...string) Histogram {
	key := joinStrings(lvs)
	if h, ok := v.histograms[key]; ok {
		return h
	}
	h := &mockHistogram{}
	v.histograms[key] = h
	return h
}

func joinStrings(ss []string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += ","
		}
		result += s
	}
	return result
}

func TestPrometheusRecorder_RecordRequest(t *testing.T) {
	requestsTotal := newMockCounterVec()
	requestDuration := newMockHistogramVec()

	recorder := NewPrometheusRecorder(PrometheusConfig{
		RequestsTotal:   requestsTotal,
		RequestDuration: requestDuration,
	})

	recorder.RecordRequest("GET", "http://example.com", 200, 100*time.Millisecond)

	counter := requestsTotal.counters["GET,http://example.com,200"]
	if counter == nil || counter.value != 1 {
		t.Error("expected request to be recorded")
	}

	histogram := requestDuration.histograms["GET,http://example.com"]
	if histogram == nil || len(histogram.values) != 1 {
		t.Error("expected duration to be recorded")
	}
}

func TestPrometheusRecorder_RecordRetry(t *testing.T) {
	retriesTotal := newMockCounterVec()

	recorder := NewPrometheusRecorder(PrometheusConfig{
		RetriesTotal: retriesTotal,
	})

	recorder.RecordRetry("GET", "http://example.com", 1)

	counter := retriesTotal.counters["GET,http://example.com,1"]
	if counter == nil || counter.value != 1 {
		t.Error("expected retry to be recorded")
	}
}

func TestPrometheusRecorder_RecordCircuitStateChange(t *testing.T) {
	circuitState := newMockGaugeVec()
	circuitTransitions := newMockCounterVec()

	recorder := NewPrometheusRecorder(PrometheusConfig{
		CircuitState:       circuitState,
		CircuitTransitions: circuitTransitions,
	})

	recorder.RecordCircuitStateChange("default", StateClosed, StateOpen)

	gauge := circuitState.gauges["default"]
	if gauge == nil || gauge.value != 1 {
		t.Error("expected circuit state to be recorded as open (1)")
	}

	counter := circuitTransitions.counters["default,closed,open"]
	if counter == nil || counter.value != 1 {
		t.Error("expected circuit transition to be recorded")
	}
}

func TestPrometheusRecorder_RecordBulkheadDenied(t *testing.T) {
	bulkheadDenied := newMockCounterVec()

	recorder := NewPrometheusRecorder(PrometheusConfig{
		BulkheadDenied: bulkheadDenied,
	})

	recorder.RecordBulkheadDenied("GET", "http://example.com")

	counter := bulkheadDenied.counters["GET,http://example.com"]
	if counter == nil || counter.value != 1 {
		t.Error("expected bulkhead denied to be recorded")
	}
}

func TestPrometheusRecorder_RecordRateLimitDenied(t *testing.T) {
	rateLimitDenied := newMockCounterVec()

	recorder := NewPrometheusRecorder(PrometheusConfig{
		RateLimitDenied: rateLimitDenied,
	})

	recorder.RecordRateLimitDenied("GET", "http://example.com")

	counter := rateLimitDenied.counters["GET,http://example.com"]
	if counter == nil || counter.value != 1 {
		t.Error("expected rate limit denied to be recorded")
	}
}

func TestPrometheusRecorder_RecordFallback(t *testing.T) {
	fallbacksTotal := newMockCounterVec()

	recorder := NewPrometheusRecorder(PrometheusConfig{
		FallbacksTotal: fallbacksTotal,
	})

	recorder.RecordFallback("GET", "http://example.com")

	counter := fallbacksTotal.counters["GET,http://example.com"]
	if counter == nil || counter.value != 1 {
		t.Error("expected fallback to be recorded")
	}
}

func TestPrometheusRecorder_RecordTimeout(t *testing.T) {
	timeoutsTotal := newMockCounterVec()

	recorder := NewPrometheusRecorder(PrometheusConfig{
		TimeoutsTotal: timeoutsTotal,
	})

	recorder.RecordTimeout("GET", "http://example.com")

	counter := timeoutsTotal.counters["GET,http://example.com"]
	if counter == nil || counter.value != 1 {
		t.Error("expected timeout to be recorded")
	}
}

func TestPrometheusRecorder_NilFields(t *testing.T) {
	recorder := NewPrometheusRecorder(PrometheusConfig{})

	recorder.RecordRequest("GET", "http://example.com", 200, 100*time.Millisecond)
	recorder.RecordRetry("GET", "http://example.com", 1)
	recorder.RecordCircuitStateChange("default", StateClosed, StateOpen)
	recorder.RecordBulkheadDenied("GET", "http://example.com")
	recorder.RecordRateLimitDenied("GET", "http://example.com")
	recorder.RecordFallback("GET", "http://example.com")
	recorder.RecordTimeout("GET", "http://example.com")
}

func TestStateToFloat(t *testing.T) {
	tests := []struct {
		state State
		want  float64
	}{
		{StateClosed, 0},
		{StateOpen, 1},
		{StateHalfOpen, 2},
		{"unknown", -1},
	}

	for _, tt := range tests {
		got := stateToFloat(tt.state)
		if got != tt.want {
			t.Errorf("stateToFloat(%s) = %f, want %f", tt.state, got, tt.want)
		}
	}
}

func TestPrometheusRecorder_Integration(t *testing.T) {
	requestsTotal := newMockCounterVec()
	retriesTotal := newMockCounterVec()
	circuitState := newMockGaugeVec()
	requestDuration := newMockHistogramVec()

	recorder := NewPrometheusRecorder(PrometheusConfig{
		RequestsTotal:   requestsTotal,
		RetriesTotal:    retriesTotal,
		CircuitState:    circuitState,
		RequestDuration: requestDuration,
	})

	client := New(
		WithMetrics(recorder),
	)

	req, _ := http.NewRequest("GET", "http://example.com", nil)
	client.Do(req)

	if len(requestsTotal.counters) == 0 {
		t.Error("expected request to be recorded")
	}
	if len(requestDuration.histograms) == 0 {
		t.Error("expected duration to be recorded")
	}
}

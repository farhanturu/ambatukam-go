package ambatukam

import (
	"net/http"
	"testing"
	"time"
)

type mockMetricsRecorder struct {
	requests        int
	retries         int
	stateChanges    int
	bulkheadDenied  int
	rateLimitDenied int
	fallbacks       int
	timeouts        int
}

func (m *mockMetricsRecorder) RecordRequest(method, url string, status int, duration time.Duration) {
	m.requests++
}
func (m *mockMetricsRecorder) RecordRetry(method, url string, attempt int) {
	m.retries++
}
func (m *mockMetricsRecorder) RecordCircuitStateChange(name string, from, to State) {
	m.stateChanges++
}
func (m *mockMetricsRecorder) RecordBulkheadDenied(method, url string) {
	m.bulkheadDenied++
}
func (m *mockMetricsRecorder) RecordRateLimitDenied(method, url string) {
	m.rateLimitDenied++
}
func (m *mockMetricsRecorder) RecordFallback(method, url string) {
	m.fallbacks++
}
func (m *mockMetricsRecorder) RecordTimeout(method, url string) {
	m.timeouts++
}

func TestMetricsRecorder_Interface(t *testing.T) {
	var recorder MetricsRecorder = &mockMetricsRecorder{}
	if recorder == nil {
		t.Fatal("expected non-nil recorder")
	}
}

func TestNoopMetricsRecorder(t *testing.T) {
	recorder := NewNoopMetricsRecorder()
	recorder.RecordRequest("GET", "http://example.com", 200, time.Millisecond)
	recorder.RecordRetry("GET", "http://example.com", 1)
	recorder.RecordCircuitStateChange("default", StateClosed, StateOpen)
	recorder.RecordBulkheadDenied("GET", "http://example.com")
	recorder.RecordRateLimitDenied("GET", "http://example.com")
	recorder.RecordFallback("GET", "http://example.com")
	recorder.RecordTimeout("GET", "http://example.com")
}

func TestMetricsRecorder_Integration(t *testing.T) {
	recorder := &mockMetricsRecorder{}
	client := New(
		WithMetrics(recorder),
	)

	req, _ := http.NewRequest("GET", "http://example.com", nil)
	client.Do(req)

	if recorder.requests != 1 {
		t.Errorf("expected 1 request recorded, got %d", recorder.requests)
	}
}

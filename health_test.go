package ambatukam

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthChecker_Handler(t *testing.T) {
	client := New()
	hc := client.HealthChecker()

	handler := hc.Handler()
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var status HealthStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if status.Status != "healthy" {
		t.Errorf("expected healthy, got %s", status.Status)
	}

	if status.Uptime <= 0 {
		t.Error("expected positive uptime")
	}
}

func TestHealthChecker_WithCircuitBreaker(t *testing.T) {
	client := New(
		WithCircuitBreaker(CircuitConfig{
			FailureThreshold: 1,
			OpenDuration:     1,
		}),
	)
	hc := client.HealthChecker()

	handler := hc.Handler()
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	var status HealthStatus
	json.NewDecoder(resp.Body).Decode(&status)

	if _, ok := status.Policies["circuit_breaker"]; !ok {
		t.Error("expected circuit_breaker in policies")
	}
}

func TestHealthChecker_WithBulkhead(t *testing.T) {
	client := New(
		WithBulkhead(BulkheadConfig{
			MaxConcurrent: 10,
		}),
	)
	hc := client.HealthChecker()

	handler := hc.Handler()
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	var status HealthStatus
	json.NewDecoder(resp.Body).Decode(&status)

	if _, ok := status.Policies["bulkhead_in_flight"]; !ok {
		t.Error("expected bulkhead_in_flight in policies")
	}
	if _, ok := status.Policies["bulkhead_denied"]; !ok {
		t.Error("expected bulkhead_denied in policies")
	}
}

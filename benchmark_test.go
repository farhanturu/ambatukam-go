package ambatukam_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/farhanturu/ambatukam-go"
)

// fastSrv returns an httptest.Server whose handler responds immediately with 200.
// Shared by all benchmarks.
func fastSrv(b *testing.B) *httptest.Server {
	b.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok")
	}))
}

// BenchmarkClient_NoPolicies — baseline: Client with no policies, plain GET.
func BenchmarkClient_NoPolicies(b *testing.B) {
	srv := fastSrv(b)
	defer srv.Close()
	c := ambatukam.New()
	defer c.Close()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		resp, err := c.Get(ctx, srv.URL)
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkClient_Retry3 — Client with retry, MaxRetries=3, server always succeeds.
// (Retry overhead when no retry is needed.)
func BenchmarkClient_Retry3(b *testing.B) {
	srv := fastSrv(b)
	defer srv.Close()
	c := ambatukam.New(ambatukam.WithRetry(ambatukam.RetryConfig{
		MaxRetries: 3,
		Backoff:    ambatukam.ConstantBackoff(1 * time.Millisecond),
	}))
	defer c.Close()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		resp, err := c.Get(ctx, srv.URL)
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkClient_FullStack — retry + circuit + timeout. All policies active.
// Circuit threshold high enough to never trip during benchmark.
func BenchmarkClient_FullStack(b *testing.B) {
	srv := fastSrv(b)
	defer srv.Close()
	c := ambatukam.New(
		ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 5 * time.Second}),
		ambatukam.WithRetry(ambatukam.RetryConfig{
			MaxRetries: 3,
			Backoff:    ambatukam.ConstantBackoff(1 * time.Millisecond),
		}),
		ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{
			FailureThreshold: 1_000_000, // effectively never trips
		}),
	)
	defer c.Close()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		resp, err := c.Get(ctx, srv.URL)
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkClient_Parallel10 — concurrent load using b.RunParallel.
func BenchmarkClient_Parallel10(b *testing.B) {
	srv := fastSrv(b)
	defer srv.Close()
	c := ambatukam.New(
		ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 2}),
		ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{FailureThreshold: 1_000_000}),
	)
	defer c.Close()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := c.Get(ctx, srv.URL)
			if err != nil {
				b.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	})
}

// BenchmarkRawHTTPClient — baseline: stdlib http.Client with no wrappers.
// Used to compute overhead multiplier of ambatukam.
func BenchmarkRawHTTPClient(b *testing.B) {
	srv := fastSrv(b)
	defer srv.Close()
	hc := &http.Client{Timeout: 5 * time.Second}
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
		resp, err := hc.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkBulkhead_Contention — bulkhead with MaxConcurrent=3, MaxQueue=0
// (fail-fast). Many callers race for slots; measures semaphore contention.
func BenchmarkBulkhead_Contention(b *testing.B) {
	srv := fastSrv(b)
	defer srv.Close()
	c := ambatukam.New(ambatukam.WithBulkhead(ambatukam.BulkheadConfig{
		MaxConcurrent: 3,
		MaxQueue:      0,
	}))
	defer c.Close()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		resp, err := c.Get(ctx, srv.URL)
		if err != nil {
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkRateLimit_WaitPath — token-bucket rate limiter with Rate=1000,
// Burst=1. Each iteration consumes a fresh token; the bucket forces a
// deterministic wait path through the limiter.
func BenchmarkRateLimit_WaitPath(b *testing.B) {
	srv := fastSrv(b)
	defer srv.Close()
	c := ambatukam.New(ambatukam.WithRateLimit(ambatukam.RateLimitConfig{
		Rate:        1000,
		Burst:       1,
		WaitTimeout: 5 * time.Second,
	}))
	defer c.Close()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		resp, err := c.Get(ctx, srv.URL)
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkCircuit_Contention — circuit breaker is pre-tripped via repeated
// 500s, then benchmark hammers a healthy server. Measures the fast-fail
// path through the open circuit.
func BenchmarkCircuit_Contention(b *testing.B) {
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer failSrv.Close()

	goodSrv := fastSrv(b)
	defer goodSrv.Close()

	c := ambatukam.New(ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{
		FailureThreshold: 1,
		OpenDuration:     1 * time.Hour,
	}))
	defer c.Close()
	ctx := context.Background()

	// Trip the breaker
	for i := 0; i < 5; i++ {
		resp, _ := c.Get(ctx, failSrv.URL)
		if resp != nil {
			resp.Body.Close()
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		resp, err := c.Get(ctx, goodSrv.URL)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}
}

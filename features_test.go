package ambatukam

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestStats_RecordsMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := New(
		WithRetry(RetryConfig{MaxRetries: 1}),
		WithTimeout(TimeoutConfig{Timeout: 2 * time.Second}),
	)
	defer client.Close()

	for i := 0; i < 5; i++ {
		resp, err := client.Get(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
	}

	stats := client.Stats()
	if stats.RequestsTotal != 5 {
		t.Fatalf("RequestsTotal=%d, want 5", stats.RequestsTotal)
	}
	if stats.Uptime <= 0 {
		t.Fatal("Uptime should be > 0")
	}
}

func TestStats_FailedRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	client := New(
		WithRetry(RetryConfig{MaxRetries: 0}),
		WithCircuitBreaker(CircuitConfig{FailureThreshold: 100}),
	)
	defer client.Close()

	for i := 0; i < 3; i++ {
		resp, err := client.Get(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
	}

	stats := client.Stats()
	if stats.RequestsFailed == 0 {
		t.Fatal("RequestsFailed should be > 0")
	}
}

func TestCache_HitAndMiss(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	client := New(
		WithCache(CacheConfig{TTL: 1 * time.Second, MaxEntries: 10}),
	)
	defer client.Close()

	for i := 0; i < 5; i++ {
		resp, err := client.Get(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		body := make([]byte, 5)
		resp.Body.Read(body)
		resp.Body.Close()
	}

	if h := hits.Load(); h != 1 {
		t.Fatalf("server hits=%d, want 1 (rest should be cached)", h)
	}

	stats := client.Stats()
	if stats.CacheHits < 4 {
		t.Fatalf("CacheHits=%d, want >= 4", stats.CacheHits)
	}
}

func TestCache_Expires(t *testing.T) {
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := New(
		WithCache(CacheConfig{TTL: 100 * time.Millisecond}),
	)
	defer client.Close()

	resp, _ := client.Get(context.Background(), srv.URL)
	resp.Body.Close()

	time.Sleep(150 * time.Millisecond)

	resp, _ = client.Get(context.Background(), srv.URL)
	resp.Body.Close()

	if c := count.Load(); c != 2 {
		t.Fatalf("count=%d, want 2 (cache should have expired)", c)
	}
}

func TestCache_SkipsNonCacheableMethods(t *testing.T) {
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := New(
		WithCache(CacheConfig{TTL: 1 * time.Second}),
	)
	defer client.Close()

	for i := 0; i < 3; i++ {
		resp, _ := client.Post(context.Background(), srv.URL, "text/plain", nil)
		resp.Body.Close()
	}

	if c := count.Load(); c != 3 {
		t.Fatalf("count=%d, want 3 (POST should not be cached)", c)
	}
}

func TestInterceptor_Called(t *testing.T) {
	var called atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := New(
		WithInterceptor(func(req *http.Request, next PolicyFunc) (*http.Response, error) {
			called.Store(true)
			req.Header.Set("X-Intercepted", "true")
			return next(req.Context(), req)
		}),
	)
	defer client.Close()

	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if !called.Load() {
		t.Fatal("interceptor was not called")
	}
}

func TestInterceptor_ShortCircuit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach server")
	}))
	defer srv.Close()

	client := New(
		WithInterceptor(func(req *http.Request, next PolicyFunc) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       http.NoBody,
			}, nil
		}),
	)
	defer client.Close()

	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestRetryBudget_LimitsRetries(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(500)
	}))
	defer srv.Close()

	client := New(
		WithRetry(RetryConfig{MaxRetries: 100}),
		WithRetryBudget(0.1, 10*time.Second),
		WithCircuitBreaker(CircuitConfig{FailureThreshold: 1000}),
	)
	defer client.Close()

	for i := 0; i < 20; i++ {
		resp, _ := client.Get(context.Background(), srv.URL)
		if resp != nil {
			resp.Body.Close()
		}
	}

	total := attempts.Load()
	if total > 25 {
		t.Fatalf("total attempts=%d, retry budget should limit retries", total)
	}
}

func TestAdaptiveTimeout_Adjusts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	at := NewAdaptiveTimeout(AdaptiveTimeoutConfig{
		Initial:    5 * time.Second,
		Percentile: 99,
		MinSamples: 5,
	})

	client := New(
		WithPolicy(at),
	)
	defer client.Close()

	for i := 0; i < 10; i++ {
		resp, err := client.Get(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
	}

	current := at.CurrentTimeout()
	if current <= 0 || current > 1*time.Second {
		t.Fatalf("adaptive timeout=%v, expected reasonable value", current)
	}
}

func TestPriorityBulkhead_PriorityRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := New(
		WithBulkhead(BulkheadConfig{
			MaxConcurrent: 1,
			MaxQueue:      5,
			QueueTimeout:  2 * time.Second,
			Priority:      true,
		}),
	)
	defer client.Close()

	ctx := WithPriority(context.Background())
	resp, err := client.Get(ctx, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestRequestLog_CapturesRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := New(
		WithRequestLog(RequestLogConfig{
			LogHeaders: []string{"Content-Type"},
		}),
	)
	defer client.Close()

	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestNewDefaultClient(t *testing.T) {
	client := NewDefaultClient()
	if client == nil {
		t.Fatal("NewDefaultClient returned nil")
	}
	defer client.Close()

	stats := client.Stats()
	if stats.Uptime <= 0 {
		t.Fatal("Uptime should be > 0")
	}
}

func TestClient_AllHTTPMethods(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := New()
	defer client.Close()

	ctx := context.Background()

	resp, err := client.Get(ctx, srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()

	resp, err = client.Head(ctx, srv.URL)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	resp.Body.Close()

	resp, err = client.Options(ctx, srv.URL)
	if err != nil {
		t.Fatalf("Options: %v", err)
	}
	resp.Body.Close()

	resp, err = client.Delete(ctx, srv.URL)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	resp.Body.Close()

	resp, err = client.Put(ctx, srv.URL, "text/plain", nil)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	resp.Body.Close()

	resp, err = client.Patch(ctx, srv.URL, "text/plain", nil)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	resp.Body.Close()
}

func TestMaxBodySize_ExceedsLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := New(
		WithRetry(RetryConfig{MaxRetries: 1, MaxBodySize: 10}),
	)
	defer client.Close()

	bigBody := make([]byte, 100)
	resp, err := client.Post(context.Background(), srv.URL, "application/octet-stream", 
		bytes.NewReader(bigBody))
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected error for oversized body")
	}
}

func TestRetryBudget_Stats(t *testing.T) {
	rb := NewRetryBudget(0.1, 10*time.Second)
	for i := 0; i < 100; i++ {
		rb.Allow()
	}
	total, retries := rb.Stats()
	if total != 100 {
		t.Fatalf("total=%d, want 100", total)
	}
	if retries > 15 {
		t.Fatalf("retries=%d, should be limited by budget", retries)
	}
}

func TestCache_LFUEviction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := New(
		WithCache(CacheConfig{TTL: 10 * time.Second, MaxEntries: 3}),
	)
	defer client.Close()

	for i := 0; i < 5; i++ {
		resp, _ := client.Get(context.Background(), srv.URL+"/item/"+string(rune('a'+i)))
		resp.Body.Close()
	}

	stats := client.Stats()
	if stats.CacheMisses < 5 {
		t.Fatalf("CacheMisses=%d, want >= 5", stats.CacheMisses)
	}
}

func TestFallback_PanicRecovery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	client := New(
		WithCircuitBreaker(CircuitConfig{FailureThreshold: 100}),
		WithFallback(FallbackConfig{
			Handler: func(req *http.Request, err error) (*http.Response, error) {
				panic("test panic in fallback")
			},
		}),
	)
	defer client.Close()

	resp, err := client.Get(context.Background(), srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected error from panic recovery")
	}
	if !errors.Is(err, ErrFallback) {
		t.Fatalf("err=%v, want ErrFallback", err)
	}
}

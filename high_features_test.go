package ambatukam_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/farhanturu/ambatukam-go"
)

// ---------------------------------------------------------------------------
// G.1 — Retry-After header parsing
// ---------------------------------------------------------------------------

func TestRetry_RespectsRetryAfter_Seconds(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "2") // 2 seconds
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(ambatukam.WithRetry(ambatukam.RetryConfig{
		MaxRetries: 3,
		Backoff:    ambatukam.ConstantBackoff(50 * time.Millisecond),
		MaxBackoff: 10 * time.Second, // high cap so Retry-After wins
	}))
	defer client.Close()

	start := time.Now()
	resp, err := client.Get(context.Background(), srv.URL)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	resp.Body.Close()
	if hits.Load() != 2 {
		t.Fatalf("hits=%d, want 2", hits.Load())
	}
	// Should wait at least 2 seconds because of Retry-After
	if elapsed < 1800*time.Millisecond {
		t.Fatalf("elapsed=%v, want >=1.8s (Retry-After: 2s)", elapsed)
	}
}

func TestRetry_RespectsRetryAfter_HTTPDate(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive")
	}

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			future := time.Now().UTC().Add(1 * time.Second)
			w.Header().Set("Retry-After", future.Format(http.TimeFormat))
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(ambatukam.WithRetry(ambatukam.RetryConfig{
		MaxRetries: 2,
		Backoff:    ambatukam.ConstantBackoff(10 * time.Millisecond),
		MaxBackoff: 5 * time.Second,
	}))
	defer client.Close()

	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	resp.Body.Close()
	if hits.Load() != 2 {
		t.Fatalf("hits=%d, want 2", hits.Load())
	}
}

func TestRetry_RetryAfterCappedAtMaxBackoff(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "9999")
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(ambatukam.WithRetry(ambatukam.RetryConfig{
		MaxRetries: 2,
		Backoff:    ambatukam.ConstantBackoff(10 * time.Millisecond),
		MaxBackoff: 100 * time.Millisecond, // cap
	}))
	defer client.Close()

	start := time.Now()
	resp, err := client.Get(context.Background(), srv.URL)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	resp.Body.Close()
	if hits.Load() != 2 {
		t.Fatalf("hits=%d, want 2", hits.Load())
	}
	// Should NOT wait 9999 seconds — capped to 100ms
	if elapsed > 1*time.Second {
		t.Fatalf("Retry-After not capped: elapsed=%v (MaxBackoff=100ms)", elapsed)
	}
}

// ---------------------------------------------------------------------------
// G.2 — PermanentError
// ---------------------------------------------------------------------------

func TestRetry_PermanentError_NoRetry(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(401)
	}))
	defer srv.Close()

	// Custom ShouldRetry that returns false on 401
	client := ambatukam.New(ambatukam.WithRetry(ambatukam.RetryConfig{
		MaxRetries: 5,
		Backoff:    ambatukam.ConstantBackoff(10 * time.Millisecond),
		ShouldRetry: func(resp *http.Response, err error) bool {
			if err != nil {
				return true
			}
			if resp != nil && resp.StatusCode == 401 {
				return false // not retryable
			}
			return resp != nil && resp.StatusCode >= 500
		},
	}))
	defer client.Close()

	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if hits.Load() != 1 {
		t.Fatalf("hits=%d, want 1 (no retry on 401)", hits.Load())
	}
}

func TestRetry_PermanentError_TypeCheck(t *testing.T) {
	// Even with default ShouldRetry, a Permanent-wrapped error short-circuits
	base := errors.New("synthetic network error")
	wrapped := ambatukam.Permanent(base)

	var perm *ambatukam.PermanentError
	if !errors.As(wrapped, &perm) {
		t.Fatalf("Permanent-wrapped error must satisfy errors.As to *PermanentError")
	}
	if perm.Err != base {
		t.Fatalf("Permanent.Err = %v, want %v", perm.Err, base)
	}
	if !strings.Contains(wrapped.Error(), "synthetic") {
		t.Fatalf("error message = %q, want contains 'synthetic'", wrapped.Error())
	}
}

// ---------------------------------------------------------------------------
// G.3 — Rich error context (RequestError)
// ---------------------------------------------------------------------------

func TestRetry_ErrorIncludesContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	client := ambatukam.New(ambatukam.WithRetry(ambatukam.RetryConfig{
		MaxRetries: 1,
		Backoff:    ambatukam.ConstantBackoff(1 * time.Millisecond),
	}))
	defer client.Close()

	_, err := client.Get(context.Background(), srv.URL+"/api/users")
	if err == nil {
		t.Fatal("expected error")
	}

	var reqErr *ambatukam.RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected RequestError in chain, got %T: %v", err, err)
	}
	if reqErr.Method != "GET" {
		t.Fatalf("Method=%s, want GET", reqErr.Method)
	}
	if !strings.Contains(reqErr.URL, "/api/users") {
		t.Fatalf("URL=%s, want contains /api/users", reqErr.URL)
	}
	if reqErr.Status != 500 {
		t.Fatalf("Status=%d, want 500", reqErr.Status)
	}
	if reqErr.Attempts != 2 {
		t.Fatalf("Attempts=%d, want 2", reqErr.Attempts)
	}
	if !errors.Is(err, ambatukam.ErrMaxRetries) {
		t.Fatal("errors.Is(err, ErrMaxRetries) must work via Unwrap")
	}
}

// ---------------------------------------------------------------------------
// G.4 — Request ID propagation
// ---------------------------------------------------------------------------

func TestRequestID_Generated(t *testing.T) {
	var received atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Store(r.Header.Get("X-Request-ID"))
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(ambatukam.WithRequestID(""))
	defer client.Close()

	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	id, _ := received.Load().(string)
	if id == "" {
		t.Fatal("X-Request-ID header not generated")
	}
	if len(id) != 24 { // 12 bytes hex-encoded
		t.Fatalf("id length=%d, want 24", len(id))
	}
}

func TestRequestID_Propagated(t *testing.T) {
	var received atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Store(r.Header.Get("X-Request-ID"))
		w.WriteHeader(200)
	}))
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("X-Request-ID", "my-custom-id-12345")
	client := ambatukam.New(ambatukam.WithRequestID(""))
	defer client.Close()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	id, _ := received.Load().(string)
	if id != "my-custom-id-12345" {
		t.Fatalf("propagated id=%q, want my-custom-id-12345", id)
	}
}

func TestRequestID_CustomHeader(t *testing.T) {
	var received atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Store(r.Header.Get("X-Correlation-Id"))
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(ambatukam.WithRequestID("X-Correlation-Id"))
	defer client.Close()

	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	id, _ := received.Load().(string)
	if id == "" {
		t.Fatal("custom header not set")
	}
}

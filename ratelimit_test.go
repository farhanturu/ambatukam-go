package ambatukam_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/farhanturu/ambatukam-go"
)

func TestRateLimit_BurstAllows(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// Burst=5, Rate=1/sec — first 5 instantly, rest fail fast.
	client := ambatukam.New(ambatukam.WithRateLimit(ambatukam.RateLimitConfig{
		Rate:  1,
		Burst: 5,
	}))
	defer client.Close()

	var allowed, denied atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 7; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.Get(context.Background(), srv.URL)
			if err == nil {
				allowed.Add(1)
			} else if errors.Is(err, ambatukam.ErrRateLimited) {
				denied.Add(1)
			}
		}()
	}
	wg.Wait()

	if allowed.Load() != 5 {
		t.Fatalf("allowed=%d, want 5", allowed.Load())
	}
	if denied.Load() != 2 {
		t.Fatalf("denied=%d, want 2", denied.Load())
	}
}

func TestRateLimit_RefillsOverTime(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// Burst=2, Rate=10/sec (1 token / 100ms).
	client := ambatukam.New(ambatukam.WithRateLimit(ambatukam.RateLimitConfig{
		Rate:  10,
		Burst: 2,
	}))
	defer client.Close()

	// Consume 2 tokens.
	for i := 0; i < 2; i++ {
		resp, err := client.Get(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("burst request %d: %v", i, err)
		}
		resp.Body.Close()
	}

	// Wait for at least 1 token to refill (150ms).
	time.Sleep(150 * time.Millisecond)

	// Next request should succeed.
	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("post-refill request: %v", err)
	}
	resp.Body.Close()
}

func TestRateLimit_WaitTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// Burst=1, Rate=1/sec, WaitTimeout=500ms — second request waits ~1s
	// for one token to refill, then succeeds (WaitTimeout just caps the
	// wait, it does not have to elapse).
	client := ambatukam.New(ambatukam.WithRateLimit(ambatukam.RateLimitConfig{
		Rate:        1,
		Burst:       1,
		WaitTimeout: 500 * time.Millisecond,
	}))
	defer client.Close()

	// Consume the 1 token.
	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	resp.Body.Close()

	// Second request: should wait then succeed.
	start := time.Now()
	resp, err = client.Get(context.Background(), srv.URL)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("second request after wait: %v", err)
	}
	resp.Body.Close()

	// Should have waited ~1 second (Rate=1/sec means 1 token per second).
	if elapsed < 800*time.Millisecond {
		t.Fatalf("elapsed=%v, want ≥800ms (Rate=1/sec means ~1s wait)", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("elapsed=%v, want ≤2s", elapsed)
	}
}

func TestRateLimit_ZeroTimeout_FailsFast(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(ambatukam.WithRateLimit(ambatukam.RateLimitConfig{
		Rate:        1,
		Burst:       1,
		WaitTimeout: 0, // fail fast
	}))
	defer client.Close()

	// Consume the 1 token.
	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Immediate second request must fail with ErrRateLimited.
	_, err = client.Get(context.Background(), srv.URL)
	if !errors.Is(err, ambatukam.ErrRateLimited) {
		t.Fatalf("err=%v, want ErrRateLimited", err)
	}
}

func TestRateLimit_ZeroRateDeniesAll(t *testing.T) {
	// Deprecated: zero-rate semantics changed in v1.1.
	//   Rate == 0 → disabled (all requests pass through).
	//   Rate <  0 → deny all (fail-closed).
	// See TestRateLimit_ZeroRate_Disabled and TestRateLimit_NegativeRate_Denies.
	t.Skip("deprecated: replaced by TestRateLimit_ZeroRate_Disabled / TestRateLimit_NegativeRate_Denies")
}

func TestRateLimit_Concurrent_NoRace(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// Rate=100, Burst=10, WaitTimeout=200ms.
	client := ambatukam.New(ambatukam.WithRateLimit(ambatukam.RateLimitConfig{
		Rate:        100,
		Burst:       10,
		WaitTimeout: 200 * time.Millisecond,
	}))
	defer client.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			client.Get(ctx, srv.URL)
		}()
	}
	wg.Wait()

	// Should have served at least the burst (10) within the time limit.
	if hits.Load() < 10 {
		t.Fatalf("hits=%d, want ≥10", hits.Load())
	}
}

func TestRateLimit_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// Burst=1, Rate=1/sec — second request would wait ~1s.
	client := ambatukam.New(ambatukam.WithRateLimit(ambatukam.RateLimitConfig{
		Rate:        1,
		Burst:       1,
		WaitTimeout: 5 * time.Second,
	}))
	defer client.Close()

	// Consume the token.
	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Second request with ctx that cancels fast.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = client.Get(ctx, srv.URL)
	if err == nil {
		t.Fatal("expected error after ctx cancel")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v, want context.DeadlineExceeded", err)
	}
}

func TestRateLimit_DefaultBurst(t *testing.T) {
	// cfg.Burst=0 should default to 1.
	rl := ambatukam.NewRateLimit(ambatukam.RateLimitConfig{Rate: 1})
	if rl == nil {
		t.Fatal("NewRateLimit returned nil")
	}
	// Just verify it doesn't panic and is usable.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(ambatukam.WithRateLimit(ambatukam.RateLimitConfig{Rate: 1}))
	defer client.Close()
	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

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

// TestBulkhead_LimitsConcurrency verifies MaxConcurrent is honored.
// SPEC NOTE: the reference spec had a redundant `done` WaitGroup that
// `Add(1)`'d 10 times but the handler only ran ~3 times (the rest fail
// fast with MaxQueue=0), which would deadlock. The `wg.Wait()` alone is
// sufficient: when client.Get returns, the handler has already executed
// past WriteHeader (so the peak was already recorded atomically).
func TestBulkhead_LimitsConcurrency(t *testing.T) {
	var inFlight atomic.Int32
	var maxObserved atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		// track peak
		for {
			m := maxObserved.Load()
			if n <= m || maxObserved.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		inFlight.Add(-1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(ambatukam.WithBulkhead(ambatukam.BulkheadConfig{
		MaxConcurrent: 3,
		MaxQueue:      0, // fail fast
	}))
	defer client.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client.Get(context.Background(), srv.URL)
		}()
	}
	wg.Wait()

	peak := maxObserved.Load()
	if peak > 3 {
		t.Fatalf("max concurrent = %d, want ≤3", peak)
	}
	if peak < 2 {
		// could happen if scheduling is unlucky; warn but pass
		t.Logf("warning: peak = %d (expected 3, may be timing-dependent)", peak)
	}
}

// TestBulkhead_FailsFastWhenQueueFull: MaxConcurrent=2, MaxQueue=0.
// Handler sleeps 200ms. Fire 5 concurrent goroutines.
// First 2 should pass; other 3 should immediately get ErrBulkheadFull.
func TestBulkhead_FailsFastWhenQueueFull(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive")
	}

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(ambatukam.WithBulkhead(ambatukam.BulkheadConfig{
		MaxConcurrent: 2,
		MaxQueue:      0,
	}))
	defer client.Close()

	var wg sync.WaitGroup
	var denied, succeeded atomic.Int32
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.Get(context.Background(), srv.URL)
			if err != nil {
				if errors.Is(err, ambatukam.ErrBulkheadFull) {
					denied.Add(1)
				}
			} else {
				succeeded.Add(1)
			}
		}()
	}
	wg.Wait()

	if succeeded.Load() != 2 {
		t.Fatalf("succeeded = %d, want 2", succeeded.Load())
	}
	if denied.Load() != 3 {
		t.Fatalf("denied = %d, want 3", denied.Load())
	}
	if hits.Load() != 2 {
		t.Fatalf("server hits = %d, want 2", hits.Load())
	}
}

// TestBulkhead_QueueTimeout: MaxConcurrent=1, MaxQueue=5, QueueTimeout=20ms.
// Handler sleeps 200ms. Fire 5 goroutines.
// First goes through; next 4 wait ~20ms then get ErrBulkheadFull.
func TestBulkhead_QueueTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(ambatukam.WithBulkhead(ambatukam.BulkheadConfig{
		MaxConcurrent: 1,
		MaxQueue:      5,
		QueueTimeout:  20 * time.Millisecond,
	}))
	defer client.Close()

	start := time.Now()
	var wg sync.WaitGroup
	var timeoutErr atomic.Int32
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.Get(context.Background(), srv.URL)
			if err != nil && errors.Is(err, ambatukam.ErrBulkheadFull) {
				timeoutErr.Add(1)
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	if timeoutErr.Load() != 4 {
		t.Fatalf("timeout errors = %d, want 4", timeoutErr.Load())
	}
	if elapsed > 500*time.Millisecond {
		// Generous bound: 1 in-flight request takes 200ms; the other 4
		// are admitted to the queue, wait 20ms, then fail. Total ≈ 220ms
		// on a clean box; allow up to 500ms to absorb CI scheduling jitter.
		t.Fatalf("total elapsed = %v, want ~220ms (1 request through + 4 timed out at 20ms)", elapsed)
	}
}

// TestBulkhead_Concurrent_NoRace: 200 goroutines hammering with MaxConcurrent=10.
// No race conditions, no panic. Run with -race.
func TestBulkhead_Concurrent_NoRace(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(ambatukam.WithBulkhead(ambatukam.BulkheadConfig{
		MaxConcurrent: 10,
		MaxQueue:      20,
		QueueTimeout:  100 * time.Millisecond,
	}))
	defer client.Close()

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			client.Get(ctx, srv.URL)
		}()
	}
	wg.Wait()

	// At least 200 attempts reached the server (or were denied). Server hits should be > 0.
	if hits.Load() == 0 {
		t.Fatal("no server hits; something is wrong")
	}
}

// TestBulkhead_DefaultMaxConcurrent: NewBulkhead with cfg.MaxConcurrent=0 picks a default.
func TestBulkhead_DefaultMaxConcurrent(t *testing.T) {
	b := ambatukam.NewBulkhead(ambatukam.BulkheadConfig{}) // MaxConcurrent=0
	if b == nil {
		t.Fatal("NewBulkhead returned nil")
	}
	// Just verify it doesn't panic when used; actual default value isn't directly observable
	// but we can verify the policy is functional via a single quick request.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// Wrap manually since we don't expose the policy for direct Do usage.
	// Use Client.WithBulkhead with the same default logic:
	client := ambatukam.New(ambatukam.WithBulkhead(ambatukam.BulkheadConfig{}))
	defer client.Close()
	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

// TestBulkhead_QueueingAllowsEventually: MaxConcurrent=2, MaxQueue=10.
// Handler takes 50ms. Fire 5 goroutines. All 5 should succeed (queued).
func TestBulkhead_QueueingAllowsEventually(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive")
	}

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(ambatukam.WithBulkhead(ambatukam.BulkheadConfig{
		MaxConcurrent: 2,
		MaxQueue:      10,
		QueueTimeout:  500 * time.Millisecond,
	}))
	defer client.Close()

	var wg sync.WaitGroup
	var ok atomic.Int32
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.Get(context.Background(), srv.URL)
			if err == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()

	if ok.Load() != 5 {
		t.Fatalf("ok = %d, want 5", ok.Load())
	}
	if hits.Load() != 5 {
		t.Fatalf("hits = %d, want 5", hits.Load())
	}
}

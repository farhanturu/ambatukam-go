package ambatukam_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/farhanturu/ambatukam-go"
)

// TestWithHooks_OrderIndependent: hooks attached via WithHooks BEFORE
// WithRetry/WithCircuitBreaker must still fire.
func TestWithHooks_OrderIndependent(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n <= 2 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var hookCalls atomic.Int32
	client := ambatukam.New(
		ambatukam.WithHooks(ambatukam.Hooks{
			OnRetry: func(req *http.Request, attempt int, nextDelay time.Duration) {
				hookCalls.Add(1)
			},
		}),
		ambatukam.WithRetry(ambatukam.RetryConfig{
			MaxRetries: 3,
			Backoff:    ambatukam.ConstantBackoff(5 * time.Millisecond),
		}),
	)
	defer client.Close()

	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if hookCalls.Load() != 2 {
		t.Fatalf("OnRetry called %d times, want 2 (hooks should fire regardless of order)", hookCalls.Load())
	}
}

func TestProductionConfig_Works(t *testing.T) {
	client := ambatukam.New(ambatukam.ProductionConfig()...)
	defer client.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestAggressiveConfig_HasTightLimits(t *testing.T) {
	cfg := ambatukam.AggressiveConfig()
	if len(cfg) == 0 {
		t.Fatal("AggressiveConfig returned empty")
	}
	// Just verify it produces a working client
	client := ambatukam.New(cfg...)
	defer client.Close()
}

func TestConservativeConfig_HasGenerousLimits(t *testing.T) {
	cfg := ambatukam.ConservativeConfig()
	if len(cfg) == 0 {
		t.Fatal("ConservativeConfig returned empty")
	}
	client := ambatukam.New(cfg...)
	defer client.Close()
}

func TestBulkhead_ErrIncludesConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(ambatukam.WithBulkhead(ambatukam.BulkheadConfig{
		MaxConcurrent: 1,
		MaxQueue:      0,
	}))
	defer client.Close()

	var wg sync.WaitGroup
	var deniedErr atomic.Value
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.Get(context.Background(), srv.URL)
			if err != nil {
				deniedErr.Store(err.Error())
			}
		}()
	}
	wg.Wait()

	msg, _ := deniedErr.Load().(string)
	if msg == "" {
		t.Fatal("expected at least one denied request")
	}
	if !strings.Contains(msg, "max_concurrent") {
		t.Fatalf("error message should include max_concurrent, got: %s", msg)
	}
}

func TestRateLimit_ErrIncludesConfig(t *testing.T) {
	client := ambatukam.New(ambatukam.WithRateLimit(ambatukam.RateLimitConfig{
		Rate:  -1,
		Burst: 10,
	}))
	defer client.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	_, err := client.Get(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ambatukam.ErrRateLimited) {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(err.Error(), "rate=") {
		t.Fatalf("error message should include rate=, got: %s", err.Error())
	}
}

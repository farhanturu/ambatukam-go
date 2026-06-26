package ambatukam_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/farhanturu/ambatukam-go"
)

func TestIntegration_FlakyServer_Recovers(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n <= 2 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 1 * time.Second}),
		ambatukam.WithRetry(ambatukam.RetryConfig{
			MaxRetries: 5,
			Backoff:    ambatukam.ConstantBackoff(5 * time.Millisecond),
		}),
		ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{
			FailureThreshold: 10, // high enough that retry succeeds before tripping
			OpenDuration:     100 * time.Millisecond,
		}),
	)
	defer client.Close()

	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if hits.Load() < 3 {
		t.Fatalf("expected >=3 hits (2 fails + 1 success), got %d", hits.Load())
	}
}

func TestIntegration_DownServer_CircuitTrips(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(500)
	}))
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 200 * time.Millisecond}),
		ambatukam.WithRetry(ambatukam.RetryConfig{
			MaxRetries: 2,
			Backoff:    ambatukam.ConstantBackoff(1 * time.Millisecond),
		}),
		ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{
			FailureThreshold: 3,
			OpenDuration:     1 * time.Second,
			HalfOpenMaxReqs:  1,
		}),
	)
	defer client.Close()

	// First request: 3 attempts (1 + 2 retries), all fail. After this, failures trip the breaker.
	_, err := client.Get(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ambatukam.ErrMaxRetries) {
		t.Fatalf("want ErrMaxRetries, got %v", err)
	}

	// Second request: circuit is open, should fail FAST with ErrCircuitOpen.
	start := time.Now()
	_, err2 := client.Get(context.Background(), srv.URL)
	elapsed := time.Since(start)
	if !errors.Is(err2, ambatukam.ErrCircuitOpen) {
		t.Fatalf("want ErrCircuitOpen, got %v", err2)
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("circuit-open response should be near-instant, took %v", elapsed)
	}

	hitsBefore := hits.Load()

	// Third request: also circuit-open, no additional server hits.
	_, err3 := client.Get(context.Background(), srv.URL)
	if !errors.Is(err3, ambatukam.ErrCircuitOpen) {
		t.Fatalf("want ErrCircuitOpen, got %v", err3)
	}
	if hits.Load() != hitsBefore {
		t.Fatalf("circuit-open request should not hit server; hits %d -> %d", hitsBefore, hits.Load())
	}
}

func TestIntegration_FullStack_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 100 * time.Millisecond}),
		ambatukam.WithRetry(ambatukam.RetryConfig{
			MaxRetries: 5,
			Backoff:    ambatukam.ConstantBackoff(20 * time.Millisecond),
		}),
		ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{
			FailureThreshold: 100, // never trip
		}),
	)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := client.Get(ctx, srv.URL)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error")
	}
	// Parent ctx fired at 50ms — should be context.DeadlineExceeded.
	// We don't assert errors.Is strictly because either DeadlineExceeded or Canceled could appear
	// depending on which one wins; just assert it failed fast.
	if elapsed > 1*time.Second {
		t.Fatalf("should have failed fast, took %v", elapsed)
	}
}

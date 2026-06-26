// Package main is a runnable example for ambatukam.
//
// It spins up an in-process flaky HTTP server and shows how to wrap a client
// with retry + circuit breaker + timeout policies. Run with:
//
//	go run ./examples/basic
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"github.com/farhanturu/ambatukam-go"
)

func main() {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "transient failure #%d", n)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok on attempt #%d", n)
	}))
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 2 * time.Second}),
		ambatukam.WithRetry(ambatukam.RetryConfig{
			MaxRetries: 3,
			// Use a tiny initial backoff so the example finishes quickly.
			Backoff: ambatukam.ConstantBackoff(10 * time.Millisecond),
		}),
		ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{
			FailureThreshold: 5,
			OpenDuration:     30 * time.Second,
		}),
	)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Get(ctx, srv.URL+"/data")
	if err != nil {
		if errors.Is(err, ambatukam.ErrMaxRetries) {
			log.Fatalf("exhausted retries: %v", err)
		}
		log.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	fmt.Printf("✓ got status %d (after %d server hits)\n", resp.StatusCode, hits.Load())
}

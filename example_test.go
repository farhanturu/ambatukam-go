package ambatukam_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/farhanturu/ambatukam-go"
)

// ExampleClient_Get demonstrates a basic GET request with all three policies.
func ExampleClient_Get() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"hello":"world"}`)
	}))
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 2 * time.Second}),
		ambatukam.WithRetry(ambatukam.DefaultRetryConfig()),
		ambatukam.WithCircuitBreaker(ambatukam.DefaultCircuitConfig()),
	)
	defer client.Close()

	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer resp.Body.Close()
	fmt.Println("status:", resp.StatusCode)
	// Output: status: 200
}

// ExampleClient_Post demonstrates a POST with a JSON body.
func ExampleClient_Post() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Println("server received:", string(body))
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client := ambatukam.New()
	defer client.Close()

	resp, err := client.Post(
		context.Background(),
		srv.URL+"/users",
		"application/json",
		io.NopCloser(strings.NewReader(`{"name":"alice"}`)),
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer resp.Body.Close()
	fmt.Println("status:", resp.StatusCode)
	// Output:
	// server received: {"name":"alice"}
	// status: 201
}

// ExampleNewRetry demonstrates configuring the retry policy.
func ExampleNewRetry() {
	cfg := ambatukam.DefaultRetryConfig()
	cfg.MaxRetries = 5
	cfg.Backoff = ambatukam.ExponentialBackoff(50*time.Millisecond, time.Second, 2.0, 0.2)

	client := ambatukam.New(ambatukam.WithRetry(cfg))
	defer client.Close()

	fmt.Println("max retries:", cfg.MaxRetries)
	// Output: max retries: 5
}

// ExampleNewCircuitBreaker demonstrates configuring the circuit breaker.
func ExampleNewCircuitBreaker() {
	cfg := ambatukam.DefaultCircuitConfig()
	cfg.FailureThreshold = 3
	cfg.OpenDuration = 10 * time.Second

	client := ambatukam.New(ambatukam.WithCircuitBreaker(cfg))
	defer client.Close()

	fmt.Println("failure threshold:", cfg.FailureThreshold)
	// Output: failure threshold: 3
}

// ExampleNewTimeout demonstrates configuring the per-attempt timeout.
func ExampleNewTimeout() {
	client := ambatukam.New(
		ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 500 * time.Millisecond}),
	)
	defer client.Close()

	fmt.Println("timeout: ok")
	// Output: timeout: ok
}

// ExampleNewBulkhead demonstrates configuring a bulkhead.
func ExampleNewBulkhead() {
	cfg := ambatukam.BulkheadConfig{
		MaxConcurrent: 5,
		MaxQueue:      10,
		QueueTimeout:  100 * time.Millisecond,
	}

	client := ambatukam.New(ambatukam.WithBulkhead(cfg))
	defer client.Close()

	fmt.Println("max concurrent:", cfg.MaxConcurrent)
	// Output: max concurrent: 5
}

// ExampleChain demonstrates manual composition of policies.
func ExampleChain() {
	client := ambatukam.New(ambatukam.WithPolicy(ambatukam.Chain(
		ambatukam.NewTimeout(ambatukam.TimeoutConfig{Timeout: 2 * time.Second}),
		ambatukam.NewRetry(ambatukam.DefaultRetryConfig()),
		ambatukam.NewCircuitBreaker(ambatukam.DefaultCircuitConfig()),
	)))
	defer client.Close()

	fmt.Println("chained: ok")
	// Output: chained: ok
}

// ExampleClient_RoundTrip shows how to use an amba *Client as the Transport
// of a standard *http.Client.
func ExampleClient_RoundTrip() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	amba := ambatukam.New(ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 2}))
	defer amba.Close()

	hc := &http.Client{Transport: amba.Transport()}
	resp, err := hc.Get(srv.URL)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer resp.Body.Close()
	fmt.Println("status:", resp.StatusCode)
	// Output: status: 200
}

// ExampleNewDefaultClient demonstrates the one-call production-default client.
func ExampleNewDefaultClient() {
	client := ambatukam.NewDefaultClient()
	defer client.Close()
	fmt.Println("ok")
	// Output: ok
}

// ExampleNewRateLimit demonstrates building a rate-limit policy directly.
func ExampleNewRateLimit() {
	client := ambatukam.New(ambatukam.WithRateLimit(ambatukam.RateLimitConfig{
		Rate:  10,
		Burst: 5,
	}))
	defer client.Close()
	fmt.Println("rate limited client ready")
	// Output: rate limited client ready
}

// ExampleNewRequestIDPolicy demonstrates registering a request-ID policy.
func ExampleNewRequestIDPolicy() {
	client := ambatukam.New(ambatukam.WithRequestIDPolicy(ambatukam.NewRequestIDPolicy()))
	defer client.Close()
	fmt.Println("request ID policy enabled")
	// Output: request ID policy enabled
}

// ExampleGetJSON demonstrates the typed JSON helper.
func ExampleGetJSON() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"name":"alice","age":30}`)
	}))
	defer srv.Close()

	client := ambatukam.New()
	defer client.Close()

	u, err := ambatukam.GetJSON[testUser](client, context.Background(), srv.URL)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(u.Name, u.Age)
	// Output: alice 30
}

// ExamplePostJSON demonstrates the typed JSON POST helper.
func ExamplePostJSON() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		fmt.Fprint(w, `{"name":"bob","age":25}`)
	}))
	defer srv.Close()

	client := ambatukam.New()
	defer client.Close()

	out, err := ambatukam.PostJSON[testUser](client, context.Background(), srv.URL, testUser{Name: "alice", Age: 30})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(out.Name, out.Age)
	// Output: bob 25
}

// ExamplePermanent demonstrates marking an error as non-retryable.
func ExamplePermanent() {
	err := ambatukam.Permanent(errors.New("do not retry me"))
	fmt.Println(err)
	// Output: permanent: do not retry me
}

// ExampleWithHTTPClient demonstrates swapping in a custom *http.Client.
func ExampleWithHTTPClient() {
	client := ambatukam.New(
		ambatukam.WithHTTPClient(&http.Client{Timeout: 5 * time.Second}),
	)
	defer client.Close()
	fmt.Println("custom http.Client")
	// Output: custom http.Client
}

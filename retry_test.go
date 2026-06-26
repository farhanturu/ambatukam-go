package ambatukam_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/farhanturu/ambatukam-go"
)

func TestRetry_SuccessAfterTransientFailures(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	cfg := ambatukam.RetryConfig{
		MaxRetries: 3,
		Backoff:    ambatukam.ConstantBackoff(1 * time.Millisecond),
	}
	c := ambatukam.New(ambatukam.WithRetry(cfg))
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body=%q, want %q", body, "ok")
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("hits=%d, want 3", got)
	}
}

func TestRetry_ExhaustsRetries(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := ambatukam.RetryConfig{
		MaxRetries: 2,
		Backoff:    ambatukam.ConstantBackoff(1 * time.Millisecond),
	}
	c := ambatukam.New(ambatukam.WithRetry(cfg))
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, err := c.Do(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ambatukam.ErrMaxRetries) {
		t.Fatalf("err=%v, want errors.Is ErrMaxRetries", err)
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("hits=%d, want 3", got)
	}
}

func TestRetry_RespectsContextCancel(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := ambatukam.RetryConfig{
		MaxRetries: 5,
		Backoff:    ambatukam.ConstantBackoff(50 * time.Millisecond),
	}
	c := ambatukam.New(ambatukam.WithRetry(cfg))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	_, err := c.Do(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want errors.Is context.Canceled", err)
	}
	if got := hits.Load(); got > 3 {
		t.Fatalf("hits=%d, want <=3", got)
	}
}

func TestRetry_NonIdempotentPOST_NoRetry(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := ambatukam.RetryConfig{
		MaxRetries: 3,
		Backoff:    ambatukam.ConstantBackoff(1 * time.Millisecond),
	}
	c := ambatukam.New(ambatukam.WithRetry(cfg))
	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader("body"))
	resp, err := c.Do(req)
	if got := hits.Load(); got != 1 {
		t.Fatalf("hits=%d, want 1", got)
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("nil response")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", resp.StatusCode)
	}
}

func TestRetry_PostBodyReplayedCorrectly(t *testing.T) {
	var (
		mu       sync.Mutex
		received []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, string(body))
		n := len(received)
		mu.Unlock()
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := ambatukam.RetryConfig{
		MaxRetries: 3,
		Backoff:    ambatukam.ConstantBackoff(1 * time.Millisecond),
		ShouldRetry: func(resp *http.Response, err error) bool {
			return resp != nil && resp.StatusCode >= 500
		},
	}
	c := ambatukam.New(ambatukam.WithRetry(cfg))
	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader("hello"))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) < 1 {
		t.Fatal("no bodies recorded")
	}
	if len(received) != 3 {
		t.Fatalf("len(received)=%d, want 3", len(received))
	}
	for i, b := range received {
		if b != "hello" {
			t.Errorf("hit %d: body=%q, want %q", i, b, "hello")
		}
	}
}

func TestRetry_4xxNotRetried(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := ambatukam.RetryConfig{
		MaxRetries: 3,
		Backoff:    ambatukam.ConstantBackoff(1 * time.Millisecond),
	}
	c := ambatukam.New(ambatukam.WithRetry(cfg))
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", resp.StatusCode)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("hits=%d, want 1", got)
	}
}

func TestRetry_CustomShouldRetry_RetriesPOST(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := ambatukam.RetryConfig{
		MaxRetries: 5,
		Backoff:    ambatukam.ConstantBackoff(1 * time.Millisecond),
		ShouldRetry: func(resp *http.Response, err error) bool {
			return resp != nil && resp.StatusCode >= 500
		},
	}
	c := ambatukam.New(ambatukam.WithRetry(cfg))
	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader("body"))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("hits=%d, want 3", got)
	}
}

func TestRetry_BackoffTiming_Approx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := ambatukam.RetryConfig{
		MaxRetries: 2,
		Backoff:    ambatukam.ConstantBackoff(20 * time.Millisecond),
	}
	c := ambatukam.New(ambatukam.WithRetry(cfg))
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)

	start := time.Now()
	_, err := c.Do(req)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("elapsed=%v, want >= 40ms", elapsed)
	}
	if elapsed > 1*time.Second {
		t.Errorf("elapsed=%v, want <= 1s", elapsed)
	}
}

func TestRetry_ChainWithOtherPolicies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var order []string
	c := ambatukam.New(
		ambatukam.WithPolicy(&dummyPolicy{name: "outer", order: &order}),
		ambatukam.WithRetry(ambatukam.RetryConfig{
			MaxRetries: 1,
			Backoff:    ambatukam.ConstantBackoff(1 * time.Millisecond),
		}),
		ambatukam.WithPolicy(&dummyPolicy{name: "inner", order: &order}),
	)
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, err := c.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"outer->in", "inner->in", "inner<-out", "outer<-out"}
	if !slicesEqual(order, want) {
		t.Fatalf("order=%v want=%v", order, want)
	}
}

func TestDefaultRetryConfig(t *testing.T) {
	cfg := ambatukam.DefaultRetryConfig()
	if cfg.MaxRetries == 0 {
		t.Fatal("defaults not set: MaxRetries is 0")
	}
	if cfg.InitialBackoff == 0 {
		t.Fatal("defaults not set: InitialBackoff is 0")
	}
	if cfg.MaxBackoff == 0 {
		t.Fatal("defaults not set: MaxBackoff is 0")
	}
	if cfg.Multiplier == 0 {
		t.Fatal("defaults not set: Multiplier is 0")
	}
}

func TestRetry_BackoffExponentialBounds(t *testing.T) {
	const jitter = 0.2
	bo := ambatukam.ExponentialBackoff(100*time.Millisecond, 1*time.Second, 2.0)

	// compare in float64 nanoseconds to avoid time.Duration integer truncation.
	check := func(attempt int, baseNanos float64) {
		minNanos := baseNanos * (1 - jitter)
		maxNanos := baseNanos * (1 + jitter)
		for sample := 0; sample < 50; sample++ {
			d := bo.NextDelay(attempt)
			dn := float64(d)
			if dn < minNanos || dn > maxNanos {
				t.Fatalf("attempt %d sample %d: got %v (%.0f ns), want within [%.0f ns, %.0f ns]",
					attempt, sample, d, dn, minNanos, maxNanos)
			}
		}
	}

	// attempts 0..3: base doubles each time, no cap yet
	for attempt := 0; attempt <= 3; attempt++ {
		base := float64(100 * time.Millisecond)
		for i := 0; i < attempt; i++ {
			base *= 2.0
		}
		check(attempt, base)
	}

	// attempts 4..10: base would exceed max, so capped at max with jitter
	maxNanos := float64(time.Second)
	for attempt := 4; attempt <= 10; attempt++ {
		check(attempt, maxNanos)
	}
}

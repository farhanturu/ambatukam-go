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

// helper: build a Client that uses a circuit breaker constructed in this
// test (so we can read its State() from outside the Client). Equivalent
// to WithCircuitBreaker but exposes the breaker reference.
func newClientWithBreaker(cb *ambatukam.CircuitBreakerPolicy) *ambatukam.Client {
	return ambatukam.New(ambatukam.WithPolicy(cb))
}

// helper: issue a GET request and return only the error, closing the body
// when a response is present.
func doGET(t *testing.T, c *ambatukam.Client, url string) error {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	if resp != nil {
		resp.Body.Close()
	}
	return nil
}

func TestCircuit_TripsAfterFailures(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := ambatukam.CircuitConfig{
		FailureThreshold: 3,
		OpenDuration:     1 * time.Hour, // never recover during test
		HalfOpenMaxReqs:  1,
	}
	c := ambatukam.New(ambatukam.WithCircuitBreaker(cfg))

	// First 3 requests pass through and hit the server (failing each time).
	for i := 0; i < 3; i++ {
		err := doGET(t, c, srv.URL)
		if err != nil {
			t.Fatalf("request %d: unexpected error %v", i+1, err)
		}
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("after 3 attempts hits=%d, want 3", got)
	}

	// 4th request: breaker should be open now.
	err := doGET(t, c, srv.URL)
	if !errors.Is(err, ambatukam.ErrCircuitOpen) {
		t.Fatalf("4th request: err=%v, want ErrCircuitOpen", err)
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("after 4th request hits=%d, want 3 (server must not be hit)", got)
	}

	// Additional requests also denied.
	for i := 0; i < 5; i++ {
		err := doGET(t, c, srv.URL)
		if !errors.Is(err, ambatukam.ErrCircuitOpen) {
			t.Fatalf("extra request %d: err=%v, want ErrCircuitOpen", i, err)
		}
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("after extra requests hits=%d, want 3", got)
	}
}

func TestCircuit_RecoversAfterOpenDuration(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := ambatukam.CircuitConfig{
		FailureThreshold: 2,
		OpenDuration:     50 * time.Millisecond,
		HalfOpenMaxReqs:  1,
	}
	cb := ambatukam.NewCircuitBreaker(cfg)
	c := newClientWithBreaker(cb)

	// 2 failures trip the breaker.
	for i := 0; i < 2; i++ {
		if err := doGET(t, c, srv.URL); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	if got := cb.State(); got != ambatukam.StateOpen {
		t.Fatalf("after trip state=%s, want open", got)
	}

	// Immediately after trip: still open.
	if err := doGET(t, c, srv.URL); !errors.Is(err, ambatukam.ErrCircuitOpen) {
		t.Fatalf("immediately after trip: err=%v, want ErrCircuitOpen", err)
	}

	// Wait for half-open window.
	time.Sleep(60 * time.Millisecond)

	// Next request: half-open trial succeeds, breaker closes.
	if err := doGET(t, c, srv.URL); err != nil {
		t.Fatalf("trial request: %v", err)
	}
	if got := cb.State(); got != ambatukam.StateClosed {
		t.Fatalf("after recovery state=%s, want closed", got)
	}

	// Subsequent requests pass through normally.
	for i := 0; i < 3; i++ {
		if err := doGET(t, c, srv.URL); err != nil {
			t.Fatalf("post-recovery request %d: %v", i, err)
		}
	}
	if got := hits.Load(); got != 6 {
		t.Fatalf("hits=%d, want 6", got)
	}
}

func TestCircuit_ReopensOnHalfOpenFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := ambatukam.CircuitConfig{
		FailureThreshold: 2,
		OpenDuration:     50 * time.Millisecond,
		HalfOpenMaxReqs:  1,
	}
	cb := ambatukam.NewCircuitBreaker(cfg)
	c := newClientWithBreaker(cb)

	// Trip.
	for i := 0; i < 2; i++ {
		_ = doGET(t, c, srv.URL)
	}
	if got := cb.State(); got != ambatukam.StateOpen {
		t.Fatalf("after trip state=%s, want open", got)
	}

	// Wait for half-open window.
	time.Sleep(60 * time.Millisecond)

	// Trial: half-open probe fails (server returns 500) -> re-opens.
	if err := doGET(t, c, srv.URL); err != nil {
		t.Fatalf("trial request: %v", err)
	}
	if got := cb.State(); got != ambatukam.StateOpen {
		t.Fatalf("after failed trial state=%s, want open", got)
	}

	// A request immediately after the failed trial must be denied.
	if err := doGET(t, c, srv.URL); !errors.Is(err, ambatukam.ErrCircuitOpen) {
		t.Fatalf("post-reopen request: err=%v, want ErrCircuitOpen", err)
	}
}

func TestCircuit_4xxDoesNotTrip(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := ambatukam.CircuitConfig{
		FailureThreshold: 3,
		OpenDuration:     1 * time.Hour,
		HalfOpenMaxReqs:  1,
	}
	cb := ambatukam.NewCircuitBreaker(cfg)
	c := newClientWithBreaker(cb)

	for i := 0; i < 5; i++ {
		if err := doGET(t, c, srv.URL); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	if got := hits.Load(); got != 5 {
		t.Fatalf("hits=%d, want 5", got)
	}
	if got := cb.State(); got != ambatukam.StateClosed {
		t.Fatalf("after 5x 404 state=%s, want closed", got)
	}
}

func TestCircuit_SuccessResetsFailureCount(t *testing.T) {
	// Sequence: F, S, F, S, F, F  =>  no three consecutive failures.
	// Server returns 500 on hits 1,3,5,6 and 200 on hits 2,4.
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		switch n {
		case 1, 3, 5, 6:
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	cfg := ambatukam.CircuitConfig{
		FailureThreshold: 3,
		OpenDuration:     1 * time.Hour,
		HalfOpenMaxReqs:  1,
	}
	cb := ambatukam.NewCircuitBreaker(cfg)
	c := newClientWithBreaker(cb)

	for i := 0; i < 6; i++ {
		if err := doGET(t, c, srv.URL); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	if got := hits.Load(); got != 6 {
		t.Fatalf("hits=%d, want 6", got)
	}
	if got := cb.State(); got != ambatukam.StateClosed {
		t.Fatalf("state=%s, want closed (no three consecutive failures)", got)
	}
}

func TestCircuit_HalfOpen_OnlyNProbes(t *testing.T) {
	var hits atomic.Int32
	release := make(chan struct{}) // barrier so admitted probes stay in-flight
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		// Only block probe requests (after the tripping phase); tripping
		// requests must return immediately so the breaker can transition.
		if n > 2 {
			<-release
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := ambatukam.CircuitConfig{
		FailureThreshold: 2,
		OpenDuration:     20 * time.Millisecond,
		HalfOpenMaxReqs:  2,
	}
	c := ambatukam.New(ambatukam.WithCircuitBreaker(cfg))

	// Trip the breaker.
	for i := 0; i < 2; i++ {
		_ = doGET(t, c, srv.URL)
	}

	// Wait for half-open window.
	time.Sleep(30 * time.Millisecond)

	// Fire 5 concurrent goroutines, gated on a barrier so they all start
	// at the same instant.
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(5)

	var (
		mu          sync.Mutex
		deniedCount int
		respCount   int
	)
	for i := 0; i < 5; i++ {
		go func() {
			defer wg.Done()
			<-start
			req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
			if err != nil {
				t.Errorf("new request: %v", err)
				return
			}
			resp, err := c.Do(req)
			mu.Lock()
			defer mu.Unlock()
			if errors.Is(err, ambatukam.ErrCircuitOpen) {
				deniedCount++
				return
			}
			if err != nil {
				t.Errorf("unexpected non-circuit error: %v", err)
				return
			}
			if resp != nil {
				respCount++
				resp.Body.Close()
			}
		}()
	}
	close(start)

	// Release the server so admitted probes can complete; then wait.
	close(release)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for goroutines")
	}

	if got := hits.Load(); got != 4 {
		// 2 hits from the tripping loop + 2 admitted half-open probes.
		t.Fatalf("server hits=%d, want 4 (2 tripping + 2 admitted probes)", got)
	}
	if deniedCount != 3 {
		t.Fatalf("deniedCount=%d, want 3", deniedCount)
	}
	if respCount != 2 {
		t.Fatalf("respCount=%d, want 2", respCount)
	}
}

func TestCircuit_Concurrent_100Goroutines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := ambatukam.CircuitConfig{
		FailureThreshold: 3,
		OpenDuration:     1 * time.Hour,
		HalfOpenMaxReqs:  1,
	}
	c := ambatukam.New(ambatukam.WithCircuitBreaker(cfg))

	// Trip the breaker synchronously.
	for i := 0; i < 3; i++ {
		_ = doGET(t, c, srv.URL)
	}

	// Fire 100 concurrent goroutines, each bounded by a per-request
	// 2-second timeout so a hung breaker would surface fast.
	var wg sync.WaitGroup
	wg.Add(100)

	var (
		mu   sync.Mutex
		errs = make([]error, 0, 100)
	)
	for i := 0; i < 100; i++ {
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				return
			}
			_, err = c.Do(req)
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for goroutines")
	}

	if len(errs) != 100 {
		t.Fatalf("got %d results, want 100", len(errs))
	}
	for i, err := range errs {
		if !errors.Is(err, ambatukam.ErrCircuitOpen) {
			t.Errorf("errs[%d]=%v, want ErrCircuitOpen", i, err)
		}
	}
}

func TestDefaultCircuitConfig(t *testing.T) {
	cfg := ambatukam.DefaultCircuitConfig()
	if cfg.FailureThreshold == 0 {
		t.Fatal("defaults not set: FailureThreshold is 0")
	}
	if cfg.OpenDuration == 0 {
		t.Fatal("defaults not set: OpenDuration is 0")
	}
	if cfg.HalfOpenMaxReqs == 0 {
		t.Fatal("defaults not set: HalfOpenMaxReqs is 0")
	}
}

func TestCircuit_StateExposed(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := ambatukam.CircuitConfig{
		FailureThreshold: 2,
		OpenDuration:     50 * time.Millisecond,
		HalfOpenMaxReqs:  1,
	}
	cb := ambatukam.NewCircuitBreaker(cfg)
	c := newClientWithBreaker(cb)

	if got := cb.State(); got != ambatukam.StateClosed {
		t.Fatalf("initial state=%s, want closed", got)
	}

	for i := 0; i < 2; i++ {
		_ = doGET(t, c, srv.URL)
	}
	if got := cb.State(); got != ambatukam.StateOpen {
		t.Fatalf("after trip state=%s, want open", got)
	}

	time.Sleep(60 * time.Millisecond)

	_ = doGET(t, c, srv.URL) // half-open trial succeeds -> closed
	if got := cb.State(); got != ambatukam.StateClosed {
		t.Fatalf("after recovery state=%s, want closed", got)
	}
}

package ambatukam

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestCircuit_GenerationRaceProtection verifies that when the breaker is
// force-reset (state changed, generation bumped) while half-open probes are
// in flight, the in-flight probes are correctly discarded by the generation
// check inside onSuccess/onFailure. None of them should panic, mutate state
// in a stale way, or trigger a data race detectable by -race.
func TestCircuit_GenerationRaceProtection(t *testing.T) {
	// Server sleeps long enough that we can reset the breaker while probes
	// are still in flight, then returns 500 to force each probe to take the
	// onFailure path.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cb := NewCircuitBreaker(CircuitConfig{
		FailureThreshold: 2,
		OpenDuration:     10 * time.Millisecond,
		HalfOpenMaxReqs:  10,
	})

	next := func(ctx context.Context, r *http.Request) (*http.Response, error) {
		return http.DefaultClient.Do(r)
	}

	// Trip the breaker.
	for i := 0; i < 2; i++ {
		req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		_, _ = cb.Execute(req.Context(), req, next)
	}
	if cb.state != StateOpen {
		t.Fatalf("after trip state=%s, want open", cb.state)
	}

	// Wait for the half-open window to open.
	time.Sleep(20 * time.Millisecond)

	// Fire 5 concurrent half-open probes. HalfOpenMaxReqs=10 so all 5
	// are admitted.
	var wg sync.WaitGroup
	wg.Add(5)
	for i := 0; i < 5; i++ {
		go func() {
			defer wg.Done()
			req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
			if err != nil {
				t.Errorf("new request: %v", err)
				return
			}
			resp, err := cb.Execute(req.Context(), req, next)
			// Each probe should complete normally (no panic, no race).
			// The result is the server's 500; the breaker should have
			// discarded this probe via the generation check.
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if resp == nil {
				t.Error("nil response")
				return
			}
			resp.Body.Close()
		}()
	}

	// Give all 5 goroutines time to enter the breaker and start their
	// server call. Then simulate an external breaker reset (e.g. an admin
	// toggle) while the probes are in flight.
	time.Sleep(50 * time.Millisecond)
	cb.mu.Lock()
	preResetGen := cb.generation.Load()
	cb.state = StateClosed
	cb.failures.Store(0)
	cb.openedAt = time.Time{}
	cb.halfOpenPermits = 0
	cb.halfOpenInFlight = 0
	cb.generation.Add(1)
	cb.mu.Unlock()

	// Wait for all in-flight probes to finish. They will call onFailure
	// with a stale genAtEntry; the generation check inside onFailure must
	// drop them without touching state. -race must report nothing.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for probes")
	}

	// After all probes complete, the forced Closed state must remain
	// untouched (probes were discarded) and the generation must have been
	// bumped exactly once by our manual reset.
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state != StateClosed {
		t.Errorf("post-completion state=%s, want closed (probes must be discarded)", cb.state)
	}
	if cb.generation.Load() != preResetGen+1 {
		t.Errorf("generation=%d, want %d (only one bump from manual reset)",
			cb.generation.Load(), preResetGen+1)
	}
}

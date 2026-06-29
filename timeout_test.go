package ambatukam_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/farhanturu/ambatukam-go"
)

// recordingPolicy records enter/exit events in a shared order slice.
type recordingPolicy struct {
	order *[]string
	count *atomic.Int32
	name  string
}

func (r *recordingPolicy) Execute(ctx context.Context, req *http.Request, next ambatukam.PolicyFunc) (*http.Response, error) {
	if r.count != nil {
		r.count.Add(1)
	}
	*r.order = append(*r.order, r.name+"->in")
	resp, err := next(ctx, req)
	*r.order = append(*r.order, r.name+"<-out")
	return resp, err
}

// shortCircuitPolicy returns an error from Execute without calling next.
// Used to verify that later policies in a Chain are not invoked.
type shortCircuitPolicy struct {
	order *[]string
	err   error
	name  string
	count *atomic.Int32
}

func (s *shortCircuitPolicy) Execute(ctx context.Context, req *http.Request, next ambatukam.PolicyFunc) (*http.Response, error) {
	if s.count != nil {
		s.count.Add(1)
	}
	*s.order = append(*s.order, s.name+"->in(blocked)")
	return nil, s.err
}

func TestTimeout_TriggersOnSlowRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := ambatukam.New(ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 50 * time.Millisecond}))
	req, _ := http.NewRequest("GET", srv.URL, nil)
	_, err := c.Do(req)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, ambatukam.ErrTimeout) {
		t.Fatalf("got %v, want wrapped ErrTimeout", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("server hits=%d, want 1", hits.Load())
	}
}

func TestTimeout_FastRequestPassesThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1 * time.Millisecond)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := ambatukam.New(ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 100 * time.Millisecond}))
	req, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestTimeout_ParentCtxCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := ambatukam.New(ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 100 * time.Millisecond}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel BEFORE making the request

	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	_, err := c.Do(req)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if errors.Is(err, ambatukam.ErrTimeout) {
		t.Fatalf("err was wrapped as ErrTimeout, but cause was parent cancel: %v", err)
	}
}

func TestTimeout_NoTimeoutWhenZero(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("slow-but-ok"))
	}))
	defer srv.Close()

	// Two variants: explicit zero and zero-value struct.
	cases := []struct {
		opt  ambatukam.Option
		name string
	}{
		{ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 0}), "explicit-zero"},
		{ambatukam.WithTimeout(ambatukam.TimeoutConfig{}), "zero-value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hits.Store(0)
			c := ambatukam.New(tc.opt)
			req, _ := http.NewRequest("GET", srv.URL, nil)
			resp, err := c.Do(req)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if resp.StatusCode != 200 {
				t.Fatalf("status=%d", resp.StatusCode)
			}
			resp.Body.Close()
			if hits.Load() != 1 {
				t.Fatalf("server hits=%d, want 1", hits.Load())
			}
		})
	}
}

func TestChain_Ordering_OuterFirst(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var order []string
	p0 := &recordingPolicy{name: "P0", order: &order}
	p1 := &recordingPolicy{name: "P1", order: &order}
	p2 := &recordingPolicy{name: "P2", order: &order}

	c := ambatukam.New(ambatukam.WithPolicy(ambatukam.Chain(p0, p1, p2)))
	req, _ := http.NewRequest("GET", srv.URL, nil)
	_, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	// Mark the terminal HTTP call explicitly by recording an extra entry
	// via a hook: simpler to just append after the call returns. Instead we
	// verify the expected ordering of recorded entries.
	want := []string{
		"P0->in", "P1->in", "P2->in",
		"P2<-out", "P1<-out", "P0<-out",
	}
	if !slicesEqual(order, want) {
		t.Fatalf("order=%v want=%v", order, want)
	}
}

func TestChain_ShortCircuitsOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be reached when chain short-circuits")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var order []string
	var p0Count, p1Count, p2Count atomic.Int32
	blocked := errors.New("blocked")

	p0 := &recordingPolicy{name: "P0", order: &order, count: &p0Count}
	p1 := &shortCircuitPolicy{name: "P1", order: &order, err: blocked, count: &p1Count}
	p2 := &recordingPolicy{name: "P2", order: &order, count: &p2Count}

	c := ambatukam.New(ambatukam.WithPolicy(ambatukam.Chain(p0, p1, p2)))
	req, _ := http.NewRequest("GET", srv.URL, nil)
	_, err := c.Do(req)
	if !errors.Is(err, blocked) {
		t.Fatalf("got %v, want %v", err, blocked)
	}
	if p0Count.Load() != 1 {
		t.Fatalf("P0 count=%d, want 1", p0Count.Load())
	}
	if p1Count.Load() != 1 {
		t.Fatalf("P1 count=%d, want 1", p1Count.Load())
	}
	if p2Count.Load() != 0 {
		t.Fatalf("P2 count=%d, want 0 (must not run)", p2Count.Load())
	}
	want := []string{"P0->in", "P1->in(blocked)", "P0<-out"}
	if !slicesEqual(order, want) {
		t.Fatalf("order=%v want=%v", order, want)
	}
}

func TestChain_EmptyChain_ReturnsError(t *testing.T) {
	// We can't really reach the empty-chain terminal through Client.Do,
	// because Client.Do always wraps its own terminal around the policy.
	// But we can call the empty Chain directly to verify its contract.
	p := ambatukam.Chain()
	// p is a ambatukam.Policy; we exercise it via a synthetic call.
	type runner interface {
		Execute(ctx context.Context, req *http.Request, next ambatukam.PolicyFunc) (*http.Response, error)
	}
	rp, ok := p.(runner)
	if !ok {
		t.Fatalf("Chain() did not return a Policy: %T", p)
	}
	noop := ambatukam.PolicyFunc(func(ctx context.Context, req *http.Request) (*http.Response, error) {
		t.Fatal("noop next should not be called by empty Chain")
		return nil, nil
	})
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	_, err := rp.Execute(req.Context(), req, noop)
	if err == nil {
		t.Fatal("expected empty-chain error")
	}
	if err.Error() != "ambatukam: empty policy chain" {
		t.Fatalf("got %v", err)
	}
}

func TestChain_SinglePolicy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var order []string
	p := &recordingPolicy{name: "only", order: &order}

	c := ambatukam.New(ambatukam.WithPolicy(ambatukam.Chain(p)))
	req, _ := http.NewRequest("GET", srv.URL, nil)
	_, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"only->in", "only<-out"}
	if !slicesEqual(order, want) {
		t.Fatalf("order=%v want=%v", order, want)
	}
}

func TestTimeout_CompositionWithCircuit(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c := ambatukam.New(
		ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{FailureThreshold: 1}),
		ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 50 * time.Millisecond}),
	)

	// First request: circuit is Closed and admits the call. Timeout fires
	// (50ms < 100ms sleep). Default shouldTrip returns true on any non-nil
	// error, so the timeout error counts as a failure and trips the breaker.
	req1, _ := http.NewRequest("GET", srv.URL, nil)
	_, err1 := c.Do(req1)
	if err1 == nil {
		t.Fatal("first request: expected timeout error")
	}
	if !errors.Is(err1, ambatukam.ErrTimeout) {
		t.Fatalf("first request: got %v, want wrapped ErrTimeout", err1)
	}

	// Second request: circuit is now Open, fails fast with ErrCircuitOpen.
	// We may need to wait out OpenDuration (zero = immediately openable),
	// but right after the trip the breaker should reject.
	// Default OpenDuration is zero, so the Open->HalfOpen transition is
	// immediate on the next call. Set a generous OpenDuration to keep it
	// fully Open during the second call.
	// Re-create the client with a non-zero OpenDuration.
	c2 := ambatukam.New(
		ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{
			FailureThreshold: 1,
			OpenDuration:     5 * time.Second,
		}),
		ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 50 * time.Millisecond}),
	)
	// Trip the breaker.
	req2, _ := http.NewRequest("GET", srv.URL, nil)
	_, err2 := c2.Do(req2)
	if !errors.Is(err2, ambatukam.ErrTimeout) {
		t.Fatalf("trip request: got %v, want wrapped ErrTimeout", err2)
	}
	// Now the breaker is Open (and stays Open for 5s).
	req3, _ := http.NewRequest("GET", srv.URL, nil)
	_, err3 := c2.Do(req3)
	if !errors.Is(err3, ambatukam.ErrCircuitOpen) {
		t.Fatalf("second request: got %v, want ErrCircuitOpen", err3)
	}
}

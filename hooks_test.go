package ambatukam_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/farhanturu/ambatukam-go"
)

type hookRecorder struct {
	mu             sync.Mutex
	beforeReqs     []*http.Request
	afterResponses []struct {
		req  *http.Request
		resp *http.Response
		err  error
	}
	retries []struct {
		req       *http.Request
		attempt   int
		nextDelay time.Duration
	}
	stateChanges []struct {
		name string
		from ambatukam.State
		to   ambatukam.State
	}
}

func newHookRecorder() *hookRecorder { return &hookRecorder{} }

func (h *hookRecorder) BeforeRequest(req *http.Request) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.beforeReqs = append(h.beforeReqs, req)
	return nil
}

func (h *hookRecorder) AfterResponse(req *http.Request, resp *http.Response, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.afterResponses = append(h.afterResponses, struct {
		req  *http.Request
		resp *http.Response
		err  error
	}{req, resp, err})
}

func (h *hookRecorder) OnRetry(req *http.Request, attempt int, nextDelay time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.retries = append(h.retries, struct {
		req       *http.Request
		attempt   int
		nextDelay time.Duration
	}{req, attempt, nextDelay})
}

func (h *hookRecorder) OnStateChange(name string, from, to ambatukam.State) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stateChanges = append(h.stateChanges, struct {
		name     string
		from, to ambatukam.State
	}{name, from, to})
}

func TestHooks_BeforeRequestMutates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization=%q, want Bearer test-token", r.Header.Get("Authorization"))
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	h := newHookRecorder()
	client := ambatukam.New(
		ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 0}),
		ambatukam.WithHooks(ambatukam.Hooks{
			BeforeRequest: func(req *http.Request) error {
				h.BeforeRequest(req)
				req.Header.Set("Authorization", "Bearer test-token")
				return nil
			},
		}),
	)
	defer client.Close()

	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(h.beforeReqs) != 1 {
		t.Fatalf("beforeReqs=%d, want 1", len(h.beforeReqs))
	}
}

func TestHooks_BeforeRequestAborts(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	want := errors.New("blocked by hook")
	client := ambatukam.New(
		ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 3}),
		ambatukam.WithHooks(ambatukam.Hooks{
			BeforeRequest: func(req *http.Request) error {
				return want
			},
		}),
	)
	defer client.Close()

	_, err := client.Get(context.Background(), srv.URL)
	if !errors.Is(err, want) {
		t.Fatalf("err=%v, want %v", err, want)
	}
	if hits.Load() != 0 {
		t.Fatalf("server hits=%d, want 0 (BeforeRequest aborted)", hits.Load())
	}
}

func TestHooks_OnRetryCalled(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= 2 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	h := newHookRecorder()
	client := ambatukam.New(
		ambatukam.WithRetry(ambatukam.RetryConfig{
			MaxRetries: 3,
			Backoff:    ambatukam.ConstantBackoff(10 * time.Millisecond),
		}),
		ambatukam.WithHooks(ambatukam.Hooks{OnRetry: h.OnRetry}),
	)
	defer client.Close()

	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if len(h.retries) != 2 {
		t.Fatalf("OnRetry called %d times, want 2", len(h.retries))
	}
	if h.retries[0].attempt != 0 || h.retries[1].attempt != 1 {
		t.Fatalf("OnRetry attempts = %v, want [0, 1]", h.retries)
	}
}

func TestHooks_AfterResponseOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	h := newHookRecorder()
	client := ambatukam.New(
		ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 0}),
		ambatukam.WithHooks(ambatukam.Hooks{AfterResponse: h.AfterResponse}),
	)
	defer client.Close()

	resp, _ := client.Get(context.Background(), srv.URL)
	if resp != nil {
		resp.Body.Close()
	}
	if len(h.afterResponses) != 1 {
		t.Fatalf("AfterResponse called %d times, want 1", len(h.afterResponses))
	}
	if h.afterResponses[0].resp == nil || h.afterResponses[0].resp.StatusCode != 500 {
		t.Fatal("AfterResponse should have the 500 response")
	}
}

func TestHooks_OnStateChangeCircuit(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(500)
	}))
	defer srv.Close()

	h := newHookRecorder()
	client := ambatukam.New(
		ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{
			FailureThreshold: 2,
			OpenDuration:     50 * time.Millisecond,
		}),
		ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 0}),
		ambatukam.WithHooks(ambatukam.Hooks{OnStateChange: h.OnStateChange}),
	)
	defer client.Close()

	for i := 0; i < 2; i++ {
		_, _ = client.Get(context.Background(), srv.URL)
	}

	time.Sleep(60 * time.Millisecond)

	_, _ = client.Get(context.Background(), srv.URL)

	if len(h.stateChanges) < 3 {
		t.Fatalf("OnStateChange called %d times, want >= 3: %+v", len(h.stateChanges), h.stateChanges)
	}
	if h.stateChanges[0].from != ambatukam.StateClosed || h.stateChanges[0].to != ambatukam.StateOpen {
		t.Fatalf("first change = %+v, want closed -> open", h.stateChanges[0])
	}
}

func TestHooks_BeforeRequestCanMutateRetriedRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Attempt-Counter") == "" {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var counter atomic.Int32
	client := ambatukam.New(
		ambatukam.WithRetry(ambatukam.RetryConfig{
			MaxRetries: 2,
			Backoff:    ambatukam.ConstantBackoff(5 * time.Millisecond),
		}),
		ambatukam.WithHooks(ambatukam.Hooks{
			BeforeRequest: func(req *http.Request) error {
				req.Header.Set("X-Attempt-Counter", strconv.Itoa(int(counter.Add(1))))
				return nil
			},
		}),
	)
	defer client.Close()

	resp, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

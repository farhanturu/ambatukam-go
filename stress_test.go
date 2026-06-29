package ambatukam_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/farhanturu/ambatukam-go"
)

// ---------------------------------------------------------------------------
// Backend test servers
// ---------------------------------------------------------------------------

type flakyServer struct {
	hits      atomic.Int32
	failUntil int32
}

func newFlakyServer(failUntil int32) *httptest.Server {
	f := &flakyServer{failUntil: failUntil}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := f.hits.Add(1)
		if n <= f.failUntil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"ok":true,"attempt":%d}`, n)
	}))
}

type slowServer struct {
	delay time.Duration
	hits  atomic.Int32
}

func newSlowServer(delay time.Duration) *httptest.Server {
	s := &slowServer{delay: delay}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		time.Sleep(s.delay)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	}))
}

func newDownServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":"down"}`)
	}))
}

func newRateLimitedServer(limit int) *httptest.Server {
	var count atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if int(count.Add(1))%limit == 0 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":"rate limited"}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	}))
}

type recoveryServer struct {
	hits    atomic.Int32
	failing atomic.Bool
}

func newRecoveryServer() *recoveryServer {
	return &recoveryServer{}
}

func (rs *recoveryServer) handler(w http.ResponseWriter, r *http.Request) {
	rs.hits.Add(1)
	if rs.failing.Load() {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"ok":true}`)
}

type chaosServer struct {
	hits       atomic.Int32
	failRate   float64
	delayMax   time.Duration
	mu         sync.Mutex
	statusCode int
}

func newChaosServer(failRate float64, delayMax time.Duration) *chaosServer {
	return &chaosServer{failRate: failRate, delayMax: delayMax, statusCode: http.StatusInternalServerError}
}

func (cs *chaosServer) handler(w http.ResponseWriter, r *http.Request) {
	cs.hits.Add(1)
	if cs.delayMax > 0 {
		time.Sleep(time.Duration(rand.Int63n(int64(cs.delayMax))))
	}
	if rand.Float64() < cs.failRate {
		cs.mu.Lock()
		code := cs.statusCode
		cs.mu.Unlock()
		w.WriteHeader(code)
		return
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"ok":true}`)
}

// ---------------------------------------------------------------------------
// Stress tests
// ---------------------------------------------------------------------------

func TestStress_FlakyServer_RetryRecovers(t *testing.T) {
	srv := newFlakyServer(3)
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithRetry(ambatukam.RetryConfig{
			MaxRetries:     5,
			InitialBackoff: 10 * time.Millisecond,
			MaxBackoff:     50 * time.Millisecond,
			Multiplier:     1.5,
			Jitter:         0.1,
		}),
	)
	defer client.Close()

	for i := 0; i < 20; i++ {
		resp, err := client.Get(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("iteration %d: status=%d, want 200", i, resp.StatusCode)
		}
	}
}

func TestStress_DownServer_CircuitTrips(t *testing.T) {
	srv := newDownServer()
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 0}),
		ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{
			FailureThreshold: 3,
			OpenDuration:     100 * time.Millisecond,
			HalfOpenMaxReqs:  1,
		}),
	)
	defer client.Close()

	for i := 0; i < 3; i++ {
		client.Get(context.Background(), srv.URL)
	}

	_, err := client.Get(context.Background(), srv.URL)
	if !errors.Is(err, ambatukam.ErrCircuitOpen) {
		t.Fatalf("err=%v, want ErrCircuitOpen", err)
	}
}

func TestStress_SlowServer_TimeoutKills(t *testing.T) {
	srv := newSlowServer(5 * time.Second)
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 50 * time.Millisecond}),
		ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 0}),
	)
	defer client.Close()

	start := time.Now()
	_, err := client.Get(context.Background(), srv.URL)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("elapsed=%v, want <500ms", elapsed)
	}
}

func TestStress_RateLimitedServer_429Retry(t *testing.T) {
	srv := newRateLimitedServer(3)
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithRetry(ambatukam.RetryConfig{
			MaxRetries:     5,
			InitialBackoff: 10 * time.Millisecond,
			MaxBackoff:     100 * time.Millisecond,
			Multiplier:     2.0,
			Jitter:         0.0,
		}),
	)
	defer client.Close()

	for i := 0; i < 10; i++ {
		resp, err := client.Get(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		resp.Body.Close()
	}
}

func TestStress_RecoveryServer_CircuitRecovers(t *testing.T) {
	rs := newRecoveryServer()
	srv := httptest.NewServer(http.HandlerFunc(rs.handler))
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 0}),
		ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{
			FailureThreshold: 3,
			OpenDuration:     100 * time.Millisecond,
			HalfOpenMaxReqs:  1,
		}),
	)
	defer client.Close()

	rs.failing.Store(true)
	for i := 0; i < 5; i++ {
		client.Get(context.Background(), srv.URL)
	}

	_, err := client.Get(context.Background(), srv.URL)
	if !errors.Is(err, ambatukam.ErrCircuitOpen) {
		t.Fatalf("expected circuit open, got %v", err)
	}

	rs.failing.Store(false)
	time.Sleep(120 * time.Millisecond)

	var ok int
	for i := 0; i < 10; i++ {
		resp, err := client.Get(context.Background(), srv.URL)
		if err == nil {
			resp.Body.Close()
			ok++
		}
	}
	if ok < 5 {
		t.Fatalf("ok=%d, want >=5 after recovery", ok)
	}
}

func TestStress_BulkheadUnderPressure(t *testing.T) {
	srv := newSlowServer(20 * time.Millisecond)
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithBulkhead(ambatukam.BulkheadConfig{
			MaxConcurrent: 5,
			MaxQueue:      0,
		}),
	)
	defer client.Close()

	const goroutines = 50
	var wg sync.WaitGroup
	var ok, denied atomic.Int32

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get(context.Background(), srv.URL)
			if err != nil {
				if errors.Is(err, ambatukam.ErrBulkheadFull) {
					denied.Add(1)
				}
				return
			}
			resp.Body.Close()
			ok.Add(1)
		}()
	}
	wg.Wait()

	if ok.Load() == 0 {
		t.Fatal("all requests denied")
	}
	if denied.Load() == 0 {
		t.Fatal("no requests denied (bulkhead not working)")
	}
	t.Logf("ok=%d denied=%d", ok.Load(), denied.Load())
}

func TestStress_RateLimiterSaturation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithRateLimit(ambatukam.RateLimitConfig{
			Rate:        10,
			Burst:       5,
			WaitTimeout: 10 * time.Millisecond,
		}),
	)
	defer client.Close()

	const goroutines = 100
	var wg sync.WaitGroup
	var ok atomic.Int32

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			resp, err := client.Get(ctx, srv.URL)
			if err != nil {
				return
			}
			resp.Body.Close()
			ok.Add(1)
		}()
	}
	wg.Wait()

	if ok.Load() == 0 {
		t.Fatal("all requests blocked")
	}
	if ok.Load() >= goroutines {
		t.Fatalf("rate limiter didn't limit: ok=%d/%d", ok.Load(), goroutines)
	}
	t.Logf("ok=%d/%d (rate limiter working)", ok.Load(), goroutines)
}

func TestStress_SingleflightDedup(t *testing.T) {
	var backendHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendHits.Add(1)
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(200)
		fmt.Fprint(w, `{"data":"shared"}`)
	}))
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithSingleflight(),
		ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 0}),
	)
	defer client.Close()

	const goroutines = 20
	var wg sync.WaitGroup
	var ok atomic.Int32

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get(context.Background(), srv.URL)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if strings.Contains(string(body), "shared") {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()

	hits := backendHits.Load()
	if hits >= goroutines {
		t.Fatalf("singleflight failed: backend hits=%d, expected < %d", hits, goroutines)
	}
	t.Logf("goroutines=%d backend_hits=%d ok=%d", goroutines, hits, ok.Load())
}

func TestStress_FallbackChain(t *testing.T) {
	srv := newDownServer()
	defer srv.Close()

	cache := map[string]string{"/api/data": `{"cached":true}`}

	client := ambatukam.New(
		ambatukam.WithFallback(ambatukam.FallbackConfig{
			Handler: func(req *http.Request, err error) (*http.Response, error) {
				if data, ok := cache[req.URL.Path]; ok {
					return &http.Response{
						StatusCode: 200,
						Body:       io.NopCloser(strings.NewReader(data)),
						Header:     http.Header{"Content-Type": []string{"application/json"}},
					}, nil
				}
				return nil, fmt.Errorf("not in cache")
			},
		}),
		ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 1, InitialBackoff: 5 * time.Millisecond}),
	)
	defer client.Close()

	resp, err := client.Get(context.Background(), srv.URL+"/api/data")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "cached") {
		t.Fatalf("body=%s, want cached response", body)
	}
}

func TestStress_FullStack_Chaos(t *testing.T) {
	cs := newChaosServer(0.5, 50*time.Millisecond)
	srv := httptest.NewServer(http.HandlerFunc(cs.handler))
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 500 * time.Millisecond}),
		ambatukam.WithRetry(ambatukam.RetryConfig{
			MaxRetries:     2,
			InitialBackoff: 10 * time.Millisecond,
			MaxBackoff:     100 * time.Millisecond,
			Multiplier:     2.0,
			Jitter:         0.2,
		}),
		ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{
			FailureThreshold: 10,
			OpenDuration:     200 * time.Millisecond,
			HalfOpenMaxReqs:  2,
		}),
		ambatukam.WithBulkhead(ambatukam.BulkheadConfig{
			MaxConcurrent: 20,
			MaxQueue:      50,
			QueueTimeout:  100 * time.Millisecond,
		}),
		ambatukam.WithRateLimit(ambatukam.RateLimitConfig{
			Rate:        200,
			Burst:       50,
			WaitTimeout: 50 * time.Millisecond,
		}),
	)
	defer client.Close()

	const goroutines = 200
	const requestsPerGoroutine = 5
	var wg sync.WaitGroup
	var ok, failed atomic.Int32

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < requestsPerGoroutine; j++ {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				resp, err := client.Get(ctx, srv.URL)
				cancel()
				if err != nil {
					failed.Add(1)
					continue
				}
				resp.Body.Close()
				ok.Add(1)
			}
		}()
	}
	wg.Wait()

	total := ok.Load() + failed.Load()
	t.Logf("total=%d ok=%d failed=%d backend_hits=%d", total, ok.Load(), failed.Load(), cs.hits.Load())
	if ok.Load() == 0 {
		t.Fatal("all requests failed")
	}
}

func TestStress_DDoS_BulkheadOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithBulkhead(ambatukam.BulkheadConfig{
			MaxConcurrent: 10,
			MaxQueue:      0,
		}),
	)
	defer client.Close()

	const goroutines = 500
	var wg sync.WaitGroup
	var ok, denied atomic.Int32

	start := time.Now()
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get(context.Background(), srv.URL)
			if err != nil {
				if errors.Is(err, ambatukam.ErrBulkheadFull) {
					denied.Add(1)
				}
				return
			}
			resp.Body.Close()
			ok.Add(1)
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	t.Logf("500 goroutines in %v: ok=%d denied=%d", elapsed, ok.Load(), denied.Load())
	if ok.Load() == 0 {
		t.Fatal("all denied")
	}
	if denied.Load() == 0 {
		t.Fatal("none denied (bulkhead broken)")
	}
}

func TestStress_DDoS_RateLimitOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithRateLimit(ambatukam.RateLimitConfig{
			Rate:        50,
			Burst:       10,
			WaitTimeout: 5 * time.Millisecond,
		}),
	)
	defer client.Close()

	const goroutines = 300
	var wg sync.WaitGroup
	var ok, limited atomic.Int32

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			resp, err := client.Get(ctx, srv.URL)
			if err != nil {
				if errors.Is(err, ambatukam.ErrRateLimited) {
					limited.Add(1)
				}
				return
			}
			resp.Body.Close()
			ok.Add(1)
		}()
	}
	wg.Wait()

	t.Logf("300 goroutines: ok=%d limited=%d", ok.Load(), limited.Load())
	if ok.Load() == 0 {
		t.Fatal("all limited")
	}
}

func TestStress_DDoS_FullStack(t *testing.T) {
	cs := newChaosServer(0.3, 20*time.Millisecond)
	srv := httptest.NewServer(http.HandlerFunc(cs.handler))
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 200 * time.Millisecond}),
		ambatukam.WithRetry(ambatukam.RetryConfig{
			MaxRetries:     2,
			InitialBackoff: 5 * time.Millisecond,
			MaxBackoff:     50 * time.Millisecond,
			Multiplier:     2.0,
			Jitter:         0.2,
		}),
		ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{
			FailureThreshold: 20,
			OpenDuration:     500 * time.Millisecond,
			HalfOpenMaxReqs:  3,
		}),
		ambatukam.WithBulkhead(ambatukam.BulkheadConfig{
			MaxConcurrent: 30,
			MaxQueue:      100,
			QueueTimeout:  200 * time.Millisecond,
		}),
		ambatukam.WithRateLimit(ambatukam.RateLimitConfig{
			Rate:        500,
			Burst:       100,
			WaitTimeout: 50 * time.Millisecond,
		}),
		ambatukam.WithSingleflight(),
	)
	defer client.Close()

	const goroutines = 1000
	var wg sync.WaitGroup
	var ok, failed atomic.Int32

	start := time.Now()
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			resp, err := client.Get(ctx, srv.URL)
			if err != nil {
				failed.Add(1)
				return
			}
			resp.Body.Close()
			ok.Add(1)
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	t.Logf("1000 goroutines in %v: ok=%d failed=%d backend_hits=%d", elapsed, ok.Load(), failed.Load(), cs.hits.Load())
	if ok.Load() == 0 {
		t.Fatal("all failed")
	}
}

func TestStress_HooksFireCorrectly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Hook") != "active" {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var beforeCount, afterCount atomic.Int32
	client := ambatukam.New(
		ambatukam.WithHooks(ambatukam.Hooks{
			BeforeRequest: func(req *http.Request) error {
				beforeCount.Add(1)
				req.Header.Set("X-Hook", "active")
				return nil
			},
			AfterResponse: func(req *http.Request, resp *http.Response, err error) {
				afterCount.Add(1)
			},
		}),
	)
	defer client.Close()

	const goroutines = 50
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get(context.Background(), srv.URL)
			if err != nil {
				return
			}
			resp.Body.Close()
		}()
	}
	wg.Wait()

	if beforeCount.Load() != goroutines {
		t.Fatalf("BeforeRequest called %d times, want %d", beforeCount.Load(), goroutines)
	}
	if afterCount.Load() != goroutines {
		t.Fatalf("AfterResponse called %d times, want %d", afterCount.Load(), goroutines)
	}
}

func TestStress_RequestIDPropagation(t *testing.T) {
	var seenIDs sync.Map
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id != "" {
			seenIDs.Store(id, true)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithRequestID("X-Request-ID"),
	)
	defer client.Close()

	const goroutines = 100
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get(context.Background(), srv.URL)
			if err != nil {
				return
			}
			resp.Body.Close()
		}()
	}
	wg.Wait()

	count := 0
	seenIDs.Range(func(_, _ any) bool { count++; return true })
	if count != goroutines {
		t.Fatalf("unique IDs=%d, want %d", count, goroutines)
	}
}

func TestStress_JSONHelpersUnderLoad(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithBulkhead(ambatukam.BulkheadConfig{MaxConcurrent: 10, MaxQueue: 0}),
	)
	defer client.Close()

	const goroutines = 50
	var wg sync.WaitGroup
	var ok atomic.Int32

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := ambatukam.GetJSON[map[string]string](client, context.Background(), srv.URL)
			if err != nil {
				return
			}
			if result["status"] == "ok" {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()

	if ok.Load() == 0 {
		t.Fatal("all JSON requests failed")
	}
}

func TestStress_TimeoutMapPerURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fast":
			w.WriteHeader(200)
		case "/slow":
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithTimeoutMap(map[string]time.Duration{
			"/fast": 1 * time.Second,
			"/slow": 50 * time.Millisecond,
		}),
		ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 0}),
	)
	defer client.Close()

	resp, err := client.Get(context.Background(), srv.URL+"/fast")
	if err != nil {
		t.Fatalf("/fast: %v", err)
	}
	resp.Body.Close()

	_, err = client.Get(context.Background(), srv.URL+"/slow")
	if err == nil {
		t.Fatal("/slow: expected timeout error")
	}
}

func TestStress_PermanentErrorStopsRetry(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(403)
	}))
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithRetry(ambatukam.RetryConfig{
			MaxRetries:     5,
			InitialBackoff: 5 * time.Millisecond,
			ShouldRetry: func(resp *http.Response, err error) bool {
				if resp != nil && resp.StatusCode == 403 {
					return false
				}
				return true
			},
		}),
	)
	defer client.Close()

	resp, _ := client.Get(context.Background(), srv.URL)
	if resp != nil {
		resp.Body.Close()
	}

	if hits.Load() != 1 {
		t.Fatalf("hits=%d, want 1 (should not retry 403)", hits.Load())
	}
}

func TestStress_PresetsSmoke(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	presets := []struct {
		name   string
		config []ambatukam.Option
	}{
		{"Production", ambatukam.ProductionConfig()},
		{"Aggressive", ambatukam.AggressiveConfig()},
		{"Conservative", ambatukam.ConservativeConfig()},
	}

	for _, p := range presets {
		t.Run(p.name, func(t *testing.T) {
			client := ambatukam.New(p.config...)
			defer client.Close()

			const goroutines = 20
			var wg sync.WaitGroup
			var ok atomic.Int32

			for i := 0; i < goroutines; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					resp, err := client.Get(context.Background(), srv.URL)
					if err != nil {
						return
					}
					resp.Body.Close()
					ok.Add(1)
				}()
			}
			wg.Wait()

			if ok.Load() == 0 {
				t.Fatal("all requests failed with preset")
			}
		})
	}
}

func TestStress_ConcurrentCircuitBreakerStateTransitions(t *testing.T) {
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(
		ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 0}),
		ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{
			FailureThreshold: 5,
			OpenDuration:     100 * time.Millisecond,
			HalfOpenMaxReqs:  2,
		}),
	)
	defer client.Close()

	for cycle := 0; cycle < 5; cycle++ {
		fail.Store(true)
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				client.Get(context.Background(), srv.URL)
			}()
		}
		wg.Wait()

		fail.Store(false)
		time.Sleep(120 * time.Millisecond)

		resp, err := client.Get(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("cycle %d: recovery failed: %v", cycle, err)
		}
		resp.Body.Close()
	}
}

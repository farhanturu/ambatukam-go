package ambatukam_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/farhanturu/ambatukam-go"
)

func TestClient_RoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 0}))
	defer client.Close()

	req, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err := client.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
}

func TestClient_Transport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 0}))
	defer client.Close()

	hc := &http.Client{Transport: client.Transport()}
	resp, err := hc.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
}

func TestNewDefaultClient(t *testing.T) {
	client := ambatukam.NewDefaultClient()
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
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
}

func TestRateLimit_ZeroRate_Disabled(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// Rate=0 means disabled, NOT deny-all.
	client := ambatukam.New(ambatukam.WithRateLimit(ambatukam.RateLimitConfig{
		Rate:  0,
		Burst: 100,
	}))
	defer client.Close()

	for i := 0; i < 50; i++ {
		resp, err := client.Get(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("req %d: %v", i, err)
		}
		resp.Body.Close()
	}
	if hits.Load() != 50 {
		t.Fatalf("hits=%d, want 50 (Rate=0 should be disabled)", hits.Load())
	}
}

func TestRateLimit_NegativeRate_Denies(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := ambatukam.New(ambatukam.WithRateLimit(ambatukam.RateLimitConfig{
		Rate:  -1,
		Burst: 100,
	}))
	defer client.Close()

	_, err := client.Get(context.Background(), srv.URL)
	if !errors.Is(err, ambatukam.ErrRateLimited) {
		t.Fatalf("err=%v, want ErrRateLimited", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("hits=%d, want 0 (Rate<0 should deny)", hits.Load())
	}
}

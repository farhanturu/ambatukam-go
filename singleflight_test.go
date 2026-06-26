package ambatukam

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSingleflight_DeduplicatesRequests(t *testing.T) {
	var callCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := New(
		WithSingleflight(),
	)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client.Get(context.Background(), ts.URL)
		}()
	}
	wg.Wait()

	if callCount.Load() != 1 {
		t.Errorf("expected 1 call, got %d", callCount.Load())
	}
}

func TestSingleflight_DifferentURLsNotDeduped(t *testing.T) {
	var callCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := New(
		WithSingleflight(),
	)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client.Get(context.Background(), ts.URL+"/path1")
			client.Get(context.Background(), ts.URL+"/path2")
		}()
	}
	wg.Wait()

	if callCount.Load() != 2 {
		t.Errorf("expected 2 calls, got %d", callCount.Load())
	}
}

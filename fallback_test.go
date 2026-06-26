package ambatukam

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFallback_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := New(
		WithFallback(FallbackConfig{
			Handler: func(req *http.Request, err error) (*http.Response, error) {
				return &http.Response{StatusCode: 200}, nil
			},
		}),
	)

	resp, err := client.Get(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestFallback_UsedOnFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	fallbackUsed := false
	client := New(
		WithFallback(FallbackConfig{
			Handler: func(req *http.Request, err error) (*http.Response, error) {
				fallbackUsed = true
				return &http.Response{StatusCode: 200}, nil
			},
		}),
		WithRetry(RetryConfig{MaxRetries: 0}),
	)

	resp, err := client.Get(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fallbackUsed {
		t.Error("expected fallback to be used")
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestFallback_WithError(t *testing.T) {
	fallbackErr := errors.New("fallback failed")
	client := New(
		WithRetry(RetryConfig{MaxRetries: 0}),
		WithFallback(FallbackConfig{
			Handler: func(req *http.Request, err error) (*http.Response, error) {
				return nil, fallbackErr
			},
		}),
	)

	_, err := client.Get(context.Background(), "http://invalid-host-that-does-not-exist")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrFallback) {
		t.Errorf("expected ErrFallback, got: %v", err)
	}
}

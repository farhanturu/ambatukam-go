package ambatukam

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTimeoutMap_ExactMatch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := New(
		WithTimeoutMap(map[string]time.Duration{
			"/api/users": 200 * time.Millisecond,
		}),
	)

	resp, err := client.Get(context.Background(), ts.URL+"/api/users")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestTimeoutMap_WildcardMatch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := New(
		WithTimeoutMap(map[string]time.Duration{
			"/api/*": 200 * time.Millisecond,
		}),
	)

	tests := []string{"/api/users", "/api/orders", "/api/products/123"}
	for _, path := range tests {
		resp, err := client.Get(context.Background(), ts.URL+path)
		if err != nil {
			t.Fatalf("unexpected error for %s: %v", path, err)
		}
		if resp.StatusCode != 200 {
			t.Errorf("expected 200 for %s, got %d", path, resp.StatusCode)
		}
	}
}

func TestTimeoutMap_NoMatch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := New(
		WithTimeoutMap(map[string]time.Duration{
			"/api/users": 200 * time.Millisecond,
		}),
	)

	resp, err := client.Get(context.Background(), ts.URL+"/other/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestTimeoutMap_TimeoutExceeded(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := New(
		WithTimeoutMap(map[string]time.Duration{
			"/api/slow": 50 * time.Millisecond,
		}),
	)

	_, err := client.Get(context.Background(), ts.URL+"/api/slow")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

func TestTimeoutMap_PriorityOverDefault(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := New(
		WithTimeoutMap(map[string]time.Duration{
			"/api/users": 300 * time.Millisecond,
		}),
	)

	resp, err := client.Get(context.Background(), ts.URL+"/api/users")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"/api/users", "/api/users", true},
		{"/api/users", "/api/orders", false},
		{"/api/*", "/api/users", true},
		{"/api/*", "/api/orders", true},
		{"/api/*", "/api/products/123", true},
		{"/api/*", "/other/path", false},
	}

	for _, tt := range tests {
		got := matchPattern(tt.pattern, tt.path)
		if got != tt.want {
			t.Errorf("matchPattern(%s, %s) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

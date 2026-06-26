package ambatukam_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/farhanturu/ambatukam-go"
)

type testUser struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestGetJSON_DecodesStruct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"name":"alice","age":30}`)
	}))
	defer srv.Close()

	client := ambatukam.New()
	defer client.Close()

	u, err := ambatukam.GetJSON[testUser](client, context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "alice" {
		t.Errorf("Name=%q, want alice", u.Name)
	}
	if u.Age != 30 {
		t.Errorf("Age=%d, want 30", u.Age)
	}
}

func TestGetJSON_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		fmt.Fprint(w, "not found")
	}))
	defer srv.Close()

	client := ambatukam.New()
	defer client.Close()

	_, err := ambatukam.GetJSON[testUser](client, context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for 404")
	}

	var reqErr *ambatukam.RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected RequestError, got %T: %v", err, err)
	}
	if reqErr.Status != 404 {
		t.Errorf("Status=%d, want 404", reqErr.Status)
	}
	if reqErr.Method != "GET" {
		t.Errorf("Method=%s, want GET", reqErr.Method)
	}
}

func TestGetJSON_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(200)
		fmt.Fprint(w, "<html>oops</html>")
	}))
	defer srv.Close()

	client := ambatukam.New()
	defer client.Close()

	_, err := ambatukam.GetJSON[testUser](client, context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected decode error")
	}
	if !errors.Is(err, err) { // sanity: not nil
		t.Fatal("error shouldn't be nil")
	}
	// Should not be a RequestError (status was 200, just bad body)
	var reqErr *ambatukam.RequestError
	if errors.As(err, &reqErr) {
		t.Fatalf("decode error should NOT be wrapped as RequestError, got %v", err)
	}
}

func TestPostJSON_RoundTrip(t *testing.T) {
	var receivedBody atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody.Store(string(body))
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type=%q, want application/json", r.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"name":"bob","age":%d}`, 25)
	}))
	defer srv.Close()

	client := ambatukam.New()
	defer client.Close()

	in := testUser{Name: "alice", Age: 30}
	out, err := ambatukam.PostJSON[testUser](client, context.Background(), srv.URL, in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "bob" {
		t.Errorf("response Name=%q, want bob", out.Name)
	}
	if out.Age != 25 {
		t.Errorf("response Age=%d, want 25", out.Age)
	}

	body, _ := receivedBody.Load().(string)
	var sent testUser
	if err := json.Unmarshal([]byte(body), &sent); err != nil {
		t.Fatalf("server didn't receive valid JSON: %v (body=%s)", err, body)
	}
	if sent.Name != "alice" || sent.Age != 30 {
		t.Errorf("server received %+v, want {alice, 30}", sent)
	}
}

func TestPostJSON_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		fmt.Fprint(w, `{"error":"invalid"}`)
	}))
	defer srv.Close()

	client := ambatukam.New()
	defer client.Close()

	_, err := ambatukam.PostJSON[testUser](client, context.Background(), srv.URL, testUser{Name: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	var reqErr *ambatukam.RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected RequestError, got %T: %v", err, err)
	}
	if reqErr.Status != 422 {
		t.Errorf("Status=%d, want 422", reqErr.Status)
	}
	if reqErr.Method != "POST" {
		t.Errorf("Method=%s, want POST", reqErr.Method)
	}
}

func TestPostJSON_NilBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"name":"x","age":0}`)
	}))
	defer srv.Close()

	client := ambatukam.New()
	defer client.Close()

	u, err := ambatukam.PostJSON[testUser](client, context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "x" {
		t.Errorf("Name=%q, want x", u.Name)
	}
}

func TestPostJSON_PassesThroughPolicies(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
		fmt.Fprint(w, `{"name":"alice","age":30}`)
	}))
	defer srv.Close()

	// Server fails once, then succeeds. Retry should kick in.
	client := ambatukam.New(ambatukam.WithRetry(ambatukam.RetryConfig{
		MaxRetries: 2,
		Backoff:    ambatukam.ConstantBackoff(5 * time.Millisecond),
		ShouldRetry: func(resp *http.Response, err error) bool {
			// POST is non-idempotent by default — opt in to retrying on 503 for this test only.
			return resp != nil && resp.StatusCode == 503
		},
	}))
	defer client.Close()

	u, err := ambatukam.PostJSON[testUser](client, context.Background(), srv.URL, testUser{Name: "alice", Age: 30})
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "alice" {
		t.Errorf("Name=%q, want alice", u.Name)
	}
	if hits.Load() != 2 {
		t.Errorf("hits=%d, want 2 (1 fail + 1 success)", hits.Load())
	}
}

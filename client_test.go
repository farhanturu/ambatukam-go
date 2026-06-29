package ambatukam_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/farhanturu/ambatukam-go"
)

// dummyPolicy: records call order
type dummyPolicy struct {
	order  *[]string
	before func(*http.Request)
	name   string
}

func (d *dummyPolicy) Execute(ctx context.Context, req *http.Request, next ambatukam.PolicyFunc) (*http.Response, error) {
	*d.order = append(*d.order, d.name+"->in")
	if d.before != nil {
		d.before(req)
	}
	resp, err := next(ctx, req)
	*d.order = append(*d.order, d.name+"<-out")
	return resp, err
}

// countingTransport: counts how many RoundTrip calls
type countingTransport struct{ count atomic.Int64 }

func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.count.Add(1)
	return http.DefaultTransport.RoundTrip(req)
}

func TestClient_Do_NoPolicies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := ambatukam.New()
	req, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestClient_Do_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer srv.Close()

	c := ambatukam.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	_, err := c.Do(req)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestClient_GetPost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			w.WriteHeader(200)
		case "POST":
			body, _ := io.ReadAll(r.Body)
			if string(body) != "hello" {
				t.Errorf("body=%q", body)
			}
			if r.Header.Get("Content-Type") != "text/plain" {
				t.Errorf("ct=%q", r.Header.Get("Content-Type"))
			}
			w.WriteHeader(201)
		}
	}))
	defer srv.Close()

	c := ambatukam.New()
	resp, err := c.Get(context.Background(), srv.URL+"/x")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("get: %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp, err = c.Post(context.Background(), srv.URL+"/x", "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 201 {
		t.Fatalf("post: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestClient_OptionChain_Single(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()

	var order []string
	c := ambatukam.New(ambatukam.WithPolicy(&dummyPolicy{name: "P1", order: &order}))
	req, _ := http.NewRequest("GET", srv.URL, nil)
	_, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"P1->in", "P1<-out"}
	if !slicesEqual(order, want) {
		t.Fatalf("order=%v want=%v", order, want)
	}
}

func TestClient_MultiplePolicies_OrderOuterFirst(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()

	var order []string
	c := ambatukam.New(
		ambatukam.WithPolicy(&dummyPolicy{name: "outer", order: &order}),
		ambatukam.WithPolicy(&dummyPolicy{name: "inner", order: &order}),
	)
	req, _ := http.NewRequest("GET", srv.URL, nil)
	_, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"outer->in", "inner->in", "inner<-out", "outer<-out"}
	if !slicesEqual(order, want) {
		t.Fatalf("order=%v want=%v", order, want)
	}
}

func TestClient_NilRequest(t *testing.T) {
	c := ambatukam.New()
	_, err := c.Do(nil)
	if !errors.Is(err, ambatukam.ErrNilRequest) {
		t.Fatalf("got %v", err)
	}
}

func TestClient_CustomHTTPClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()

	tr := &countingTransport{}
	hc := &http.Client{Transport: tr}
	c := ambatukam.New(ambatukam.WithHTTPClient(hc))
	req, _ := http.NewRequest("GET", srv.URL, nil)
	_, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if tr.count.Load() != 1 {
		t.Fatalf("count=%d", tr.count.Load())
	}
}

func TestClient_Close(t *testing.T) {
	c := ambatukam.New()
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestClient_DoWithContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := ambatukam.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, err := c.DoWithContext(ctx, req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestClient_DoWithContext_NilRequest(t *testing.T) {
	c := ambatukam.New()
	_, err := c.DoWithContext(context.Background(), nil)
	if !errors.Is(err, ambatukam.ErrNilRequest) {
		t.Fatalf("got %v, want ErrNilRequest", err)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

package ambatukam

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

type Client struct {
	hc       *http.Client
	policies []Policy
	logger   *slog.Logger
	hooks    Hooks
}

func New(opts ...Option) *Client {
	c := &Client{
		hc:     http.DefaultClient,
		logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(c)
	}
	for _, p := range c.policies {
		switch pp := p.(type) {
		case *RetryPolicy:
			pp.WithHooks(c.hooks)
		case *CircuitBreakerPolicy:
			pp.WithHooks(c.hooks)
		}
	}
	return c
}

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("ambatukam.Do: %w", ErrNilRequest)
	}

	funcs := make([]PolicyFunc, len(c.policies)+1)
	funcs[len(funcs)-1] = func(ctx context.Context, r *http.Request) (*http.Response, error) {
		return c.hc.Do(r)
	}
	for i := len(c.policies) - 1; i >= 0; i-- {
		p := c.policies[i]
		next := funcs[i+1]
		funcs[i] = func(ctx context.Context, r *http.Request) (*http.Response, error) {
			return p.Execute(ctx, r, next)
		}
	}

	return funcs[0](req.Context(), req)
}

func (c *Client) Get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

func (c *Client) Post(ctx context.Context, url, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return c.Do(req)
}

func (c *Client) DoWithContext(ctx context.Context, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("ambatukam.DoWithContext: %w", ErrNilRequest)
	}
	return c.Do(req.WithContext(ctx))
}

func (c *Client) Close() error {
	c.hc.CloseIdleConnections()
	return nil
}

func (c *Client) RoundTrip(req *http.Request) (*http.Response, error) {
	return c.Do(req)
}

func (c *Client) Transport() http.RoundTripper {
	return roundTripperFunc(c.RoundTrip)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

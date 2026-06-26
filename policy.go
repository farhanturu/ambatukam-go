package ambatukam

import (
	"context"
	"errors"
	"net/http"
)

type Policy interface {
	Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error)
}

type PolicyFunc func(ctx context.Context, req *http.Request) (*http.Response, error)

func (f PolicyFunc) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	return f(ctx, req)
}

func Chain(policies ...Policy) Policy {
	if len(policies) == 0 {
		return PolicyFunc(func(ctx context.Context, req *http.Request) (*http.Response, error) {
			return nil, errors.New("ambatukam: empty policy chain")
		})
	}
	if len(policies) == 1 {
		return policies[0]
	}
	return &chainPolicy{policies: policies}
}

type chainPolicy struct {
	policies []Policy
}

func (c *chainPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	fn := next
	for i := len(c.policies) - 1; i >= 0; i-- {
		p := c.policies[i]
		inner := fn
		fn = func(ctx context.Context, req *http.Request) (*http.Response, error) {
			return p.Execute(ctx, req, inner)
		}
	}
	return fn(ctx, req)
}

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

// Execute makes PolicyFunc satisfy the Policy interface so a bare function
// can be used wherever a Policy is expected (e.g. WithPolicy or as a return
// value from Chain). The function is invoked directly; `next` is intentionally
// ignored — a PolicyFunc is treated as a terminal behavior.
func (f PolicyFunc) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	return f(ctx, req)
}

// Chain composes policies so the FIRST argument is OUTERMOST.
// Execution order: P0.Execute -> P1.Execute -> ... -> Pn.Execute -> next.
// Each policy sees the request before passing to next, and sees the
// result/error after.
//
// The returned Policy delegates Execute by threading the supplied `next`
// into the innermost policy, so when used via WithPolicy(Chain(...)) the
// Client.Do chain builder's terminal (c.hc.Do) reaches the last policy in
// the chain.
func Chain(policies ...Policy) Policy {
	if len(policies) == 0 {
		// Empty chain has no terminal; if it ever runs it returns a
		// descriptive error rather than panicking.
		return PolicyFunc(func(ctx context.Context, req *http.Request) (*http.Response, error) {
			return nil, errors.New("ambatukam: empty policy chain")
		})
	}
	if len(policies) == 1 {
		return policies[0]
	}
	return &chainPolicy{policies: policies}
}

// chainPolicy is the concrete Policy returned by Chain for len >= 2.
// Its Execute builds an inner PolicyFunc ladder from the policies, with
// the outer `next` argument wired to the innermost policy's next slot.
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

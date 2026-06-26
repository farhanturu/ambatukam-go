package ambatukam

import (
	"context"
	"net/http"
	"sync"
)

type SingleflightPolicy struct {
	mu    sync.Mutex
	calls map[string]*singleflightCall
}

type singleflightCall struct {
	wg  sync.WaitGroup
	val *http.Response
	err error
}

func NewSingleflight() *SingleflightPolicy {
	return &SingleflightPolicy{calls: make(map[string]*singleflightCall)}
}

func (sf *SingleflightPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	key := req.Method + " " + req.URL.String()

	sf.mu.Lock()
	if c, ok := sf.calls[key]; ok {
		sf.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}

	c := &singleflightCall{}
	c.wg.Add(1)
	sf.calls[key] = c
	sf.mu.Unlock()

	c.val, c.err = next(ctx, req)
	c.wg.Done()

	sf.mu.Lock()
	delete(sf.calls, key)
	sf.mu.Unlock()

	return c.val, c.err
}

package ambatukam

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

const defaultRequestIDHeader = "X-Request-ID"

type RequestIDPolicy struct {
	header string
	gen    func() string
}

func NewRequestIDPolicy() *RequestIDPolicy {
	return &RequestIDPolicy{header: defaultRequestIDHeader, gen: defaultIDGenerator}
}

func (r *RequestIDPolicy) WithHeader(name string) *RequestIDPolicy {
	if name != "" {
		r.header = name
	}
	return r
}

func (r *RequestIDPolicy) WithGenerator(gen func() string) *RequestIDPolicy {
	if gen != nil {
		r.gen = gen
	}
	return r
}

func (r *RequestIDPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	if req.Header.Get(r.header) == "" {
		req.Header.Set(r.header, r.gen())
	}
	return next(ctx, req)
}

func defaultIDGenerator() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

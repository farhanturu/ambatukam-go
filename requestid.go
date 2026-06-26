package ambatukam

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

const defaultRequestIDHeader = "X-Request-ID"

// RequestIDPolicy adds or propagates a request ID header on every request.
// Useful for distributed tracing and log correlation across services.
type RequestIDPolicy struct {
	header string
	gen    func() string
}

// NewRequestIDPolicy returns a RequestIDPolicy that uses the default
// "X-Request-ID" header and an internal hex-encoded random generator.
// Use WithHeader and WithGenerator to customise.
func NewRequestIDPolicy() *RequestIDPolicy {
	return &RequestIDPolicy{header: defaultRequestIDHeader, gen: defaultIDGenerator}
}

// WithHeader overrides the header name (default "X-Request-ID").
// Empty names are ignored to keep a sane default.
func (r *RequestIDPolicy) WithHeader(name string) *RequestIDPolicy {
	if name != "" {
		r.header = name
	}
	return r
}

// WithGenerator overrides the ID generator (e.g., for UUID v7).
// Nil generators are ignored.
func (r *RequestIDPolicy) WithGenerator(gen func() string) *RequestIDPolicy {
	if gen != nil {
		r.gen = gen
	}
	return r
}

// Execute sets the configured request ID header on req when not already
// present, then invokes next.
func (r *RequestIDPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	if req.Header.Get(r.header) == "" {
		req.Header.Set(r.header, r.gen())
	}
	return next(ctx, req)
}

// defaultIDGenerator returns 12 random bytes hex-encoded (24 chars).
func defaultIDGenerator() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

package ambatukam

import (
	"errors"
	"fmt"
)

var (
	ErrCircuitOpen  = errors.New("ambatukam: circuit breaker is open")
	ErrMaxRetries   = errors.New("ambatukam: max retries exceeded")
	ErrNilRequest   = errors.New("ambatukam: nil request")
	ErrTimeout      = errors.New("ambatukam: per-attempt timeout exceeded")
	ErrBulkheadFull = errors.New("ambatukam: bulkhead full")
	ErrRateLimited  = errors.New("ambatukam: rate limited")
)

// PermanentError signals that a retry must NOT be attempted for this error.
// Wrap any error with Permanent() to mark it as non-retryable; the retry
// policy will propagate it immediately without further attempts.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string {
	if e.Err == nil {
		return "permanent error"
	}
	return "permanent: " + e.Err.Error()
}

func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err to mark it as non-retryable.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// RequestError captures full context about a failed request:
// method, URL, status code, attempt count, and the underlying error.
type RequestError struct {
	Method   string
	URL      string
	Status   int
	Attempts int
	Err      error
}

func (e *RequestError) Error() string {
	parts := fmt.Sprintf("ambatukam: %s %s after %d attempt(s)", e.Method, e.URL, e.Attempts)
	if e.Status > 0 {
		parts = fmt.Sprintf("ambatukam: %s %s returned status %d after %d attempt(s)", e.Method, e.URL, e.Status, e.Attempts)
	}
	if e.Err != nil {
		return parts + ": " + e.Err.Error()
	}
	return parts
}

func (e *RequestError) Unwrap() error { return e.Err }

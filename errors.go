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
	ErrFallback     = errors.New("ambatukam: fallback failed")
)

type PermanentError struct{ Err error }

func (e *PermanentError) Error() string {
	if e.Err == nil {
		return "permanent error"
	}
	return "permanent: " + e.Err.Error()
}

func (e *PermanentError) Unwrap() error { return e.Err }

func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

type RequestError struct {
	Method   string
	URL      string
	Err      error
	Policy   string
	Status   int
	Attempts int
}

func (e *RequestError) Error() string {
	prefix := "ambatukam"
	if e.Policy != "" {
		prefix += "[" + e.Policy + "]"
	}
	parts := fmt.Sprintf("%s: %s %s after %d attempt(s)", prefix, e.Method, e.URL, e.Attempts)
	if e.Status > 0 {
		parts = fmt.Sprintf("%s: %s %s returned status %d after %d attempt(s)", prefix, e.Method, e.URL, e.Status, e.Attempts)
	}
	if e.Err != nil {
		return parts + ": " + e.Err.Error()
	}
	return parts
}

func (e *RequestError) Unwrap() error { return e.Err }

package ambatukam

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// FuzzParseRetryAfter exercises parseRetryAfter across a wide range of
// Retry-After header values. It must never panic and must always return a
// non-negative duration bounded to a sane range.
func FuzzParseRetryAfter(f *testing.F) {
	// Seed corpus with common inputs
	f.Add("0")
	f.Add("1")
	f.Add("30")
	f.Add("999999")
	f.Add("-1")                      // negative
	f.Add("abc")                     // non-numeric
	f.Add("")                        // empty
	f.Add("1.5")                     // float
	f.Add("99999999999999999999999") // overflow
	f.Add(time.Now().UTC().Add(time.Hour).Format(http.TimeFormat))
	f.Add(time.Now().UTC().Add(-time.Hour).Format(http.TimeFormat)) // past date
	f.Add("Mon, 02 Jan 2006 15:04:05 GMT")
	f.Add("not a date")
	f.Add("0\r\n") // CRLF

	f.Fuzz(func(t *testing.T, header string) {
		// Construct minimal response with the header
		resp := &http.Response{Header: http.Header{}}
		resp.Header.Set("Retry-After", header)

		// Should not panic, should always return non-negative duration
		d := parseRetryAfter(resp)
		if d < 0 {
			t.Fatalf("parseRetryAfter(%q) = %v, want non-negative", header, d)
		}
		// Should never exceed 1 year (sanity)
		if d > 365*24*time.Hour {
			t.Fatalf("parseRetryAfter(%q) = %v, suspiciously large", header, d)
		}
	})
}

// FuzzShouldRetryDefault exercises the default shouldRetry heuristic across
// arbitrary method/status combinations. It must never panic.
func FuzzShouldRetryDefault(f *testing.F) {
	// Seed corpus with common method/status combinations
	f.Add("GET", 200)
	f.Add("GET", 500)
	f.Add("POST", 500)
	f.Add("PUT", 404)
	f.Add("DELETE", 503)
	f.Add("HEAD", 200)
	f.Add("OPTIONS", 502)
	f.Add("PATCH", 500)
	f.Add("", 500) // empty method

	f.Fuzz(func(t *testing.T, method string, status int) {
		// Build a policy with default ShouldRetry
		p := NewRetry(RetryConfig{MaxRetries: 0})
		// Normalize method to uppercase
		m := strings.ToUpper(method)

		req, _ := http.NewRequest(m, "http://example.com", nil)
		var resp *http.Response
		if status > 0 {
			resp = &http.Response{StatusCode: status}
		}

		// Should not panic
		_ = p.shouldRetry(req, resp, nil)
	})
}

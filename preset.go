package ambatukam

import (
	"runtime"
	"time"
)

// ProductionConfig returns a balanced default configuration suitable for most
// production services. Use with New():
//
//	client := ambatukam.New(ambatukam.ProductionConfig()...)
//
// Tuning: 3 retries, 30s timeout, 5-failure circuit, NumCPU*4 concurrent.
func ProductionConfig() []Option {
	return DefaultConfig() // alias — same as DefaultConfig
}

// AggressiveConfig returns a strict, fast-fail configuration for protecting
// fragile downstream services. Lower retry count, lower circuit threshold,
// short timeouts.
func AggressiveConfig() []Option {
	return []Option{
		WithRetry(RetryConfig{
			MaxRetries:     1,
			InitialBackoff: 50 * time.Millisecond,
			MaxBackoff:     500 * time.Millisecond,
			Multiplier:     2.0,
			Jitter:         0.2,
		}),
		WithCircuitBreaker(CircuitConfig{
			FailureThreshold: 3,
			OpenDuration:     10 * time.Second,
			HalfOpenMaxReqs:  1,
		}),
		WithTimeout(TimeoutConfig{Timeout: 5 * time.Second}),
		WithBulkhead(BulkheadConfig{
			MaxConcurrent: uint32(runtime.NumCPU() * 2),
			MaxQueue:      0, // fail fast
		}),
	}
}

// ConservativeConfig returns a generous configuration for critical services
// that must not fail. Many retries, slow trip, larger timeouts.
func ConservativeConfig() []Option {
	return []Option{
		WithRetry(RetryConfig{
			MaxRetries:     5,
			InitialBackoff: 200 * time.Millisecond,
			MaxBackoff:     30 * time.Second,
			Multiplier:     2.0,
			Jitter:         0.2,
		}),
		WithCircuitBreaker(CircuitConfig{
			FailureThreshold: 20,
			OpenDuration:     60 * time.Second,
			HalfOpenMaxReqs:  3,
		}),
		WithTimeout(TimeoutConfig{Timeout: 60 * time.Second}),
		WithBulkhead(BulkheadConfig{
			MaxConcurrent: uint32(runtime.NumCPU() * 8),
			MaxQueue:      200,
			QueueTimeout:  500 * time.Millisecond,
		}),
	}
}

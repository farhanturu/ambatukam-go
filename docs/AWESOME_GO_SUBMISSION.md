# Awesome Go Submission

## Library Information

- **Name:** Ambatukam Go
- **URL:** https://github.com/farhanturu/ambatukam-go
- **Description:** Composable, idiomatic Go HTTP resilience — Retry, Circuit Breaker, Bulkhead, Rate Limiter, Timeout, Fallback, Singleflight, Hooks, Metrics, Health Check. Zero dependencies.
- **Category:** HTTP Clients / Resilience

## Why This Library Should Be Included

1. **Zero dependencies** — Unlike other resilience libraries, Ambatukam Go has zero external dependencies
2. **Comprehensive** — Combines 10+ resilience patterns in one library
3. **Production-grade** — Used in production with 80+ tests, race-safe, benchmarked
4. **Idiomatic Go** — Follows Go conventions, uses generics, slog, atomic operations
5. **Well-documented** — README, COOKBOOK, FAQ, MIGRATION guides

## Features

- Retry with exponential/constant/linear backoff + jitter
- Circuit breaker (closed/open/half-open state machine)
- Bulkhead (concurrency limiter with bounded queue)
- Rate limiter (token bucket)
- Per-attempt timeout
- Fallback strategy
- Singleflight (request deduplication)
- Request ID propagation
- Hooks (BeforeRequest, AfterResponse, OnRetry, OnStateChange, OnFallback)
- Generic JSON helpers (GetJSON[T], PostJSON[T])
- Health check endpoint
- Metrics interface (Prometheus-ready)
- Preset configs (Production, Aggressive, Conservative)

## Example

```go
client := ambatukam.New(
    ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 2 * time.Second}),
    ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 3}),
    ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{FailureThreshold: 5}),
    ambatukam.WithBulkhead(ambatukam.BulkheadConfig{MaxConcurrent: 10}),
    ambatukam.WithRateLimit(ambatukam.RateLimitConfig{Rate: 10, Burst: 5}),
)
defer client.Close()

resp, err := client.Get(context.Background(), "https://api.example.com/users")
```

## Links

- **Go Reference:** https://pkg.go.dev/github.com/farhanturu/ambatukam-go
- **GitHub:** https://github.com/farhanturu/ambatukam-go
- **License:** MIT

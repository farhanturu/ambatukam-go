# Migration Guide

This guide helps you migrate from other Go HTTP resilience libraries to Ambatukam Go.

If you don't see your library here, please open an issue.

---

## From `cenkalti/backoff`

`cenkalti/backoff` is a retry-only library. To get the full resilience stack (circuit breaker, bulkhead, etc.), you'd typically pair it with `sony/gobreaker`.

### Before

```go
import "github.com/cenkalti/backoff/v4"

bo := backoff.NewExponentialBackOff()
bo.MaxElapsedTime = 30 * time.Second

operation := func() error {
    resp, err := http.Get(url)
    if err != nil { return err }
    defer resp.Body.Close()
    if resp.StatusCode >= 500 { return fmt.Errorf("status %d", resp.StatusCode) }
    return nil
}

err := backoff.Retry(operation, bo)
```

### After

```go
import "github.com/farhanturu/ambatukam-go"

client := ambatukam.New(ambatukam.WithRetry(ambatukam.RetryConfig{
    MaxRetries:     3,
    InitialBackoff: 100 * time.Millisecond,
    MaxBackoff:     30 * time.Second,
    Multiplier:     2.0,
}))

resp, err := client.Get(ctx, url)
```

### What you gain
- Circuit breaker (not in `backoff` alone)
- Body buffering (POST retry safety)
- `Retry-After` header support
- Cleaner context propagation

### What you give up
- `backoff.WithContext` is just ctx; no special API
- `backoff.PermanentError` → `ambatukam.Permanent(err)`

---

## From `sony/gobreaker`

`sony/gobreaker` is circuit-breaker-only. Pair with `backoff` for retry.

### Before

```go
import "github.com/sony/gobreaker"

cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
    Name:        "my-service",
    MaxRequests: 1,
    Interval:    60 * time.Second,
    Timeout:     30 * time.Second,
    ReadyToTrip: func(counts gobreaker.Counts) bool {
        return counts.ConsecutiveFailures > 5
    },
})

result, err := cb.Execute(func() (interface{}, error) {
    resp, err := http.Get(url)
    if err != nil { return nil, err }
    defer resp.Body.Close()
    if resp.StatusCode >= 500 {
        return nil, fmt.Errorf("status %d", resp.StatusCode)
    }
    return resp, nil
})
```

### After

```go
import "github.com/farhanturu/ambatukam-go"

client := ambatukam.New(ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{
    FailureThreshold: 5,
    OpenDuration:     30 * time.Second,
    HalfOpenMaxReqs:  1,
}))

resp, err := client.Get(ctx, url)
```

### Field mapping
| gobreaker | ambatukam |
|---|---|
| `Name` | (use `WithName` on the breaker; default "default") |
| `MaxRequests` (half-open permits) | `HalfOpenMaxReqs` |
| `Interval` | (not directly exposed; open state doesn't have periodic reset) |
| `Timeout` | `OpenDuration` |
| `ReadyToTrip` | `ShouldTrip` |

### What you gain
- Full retry+circuit+timeout stack with one config
- Hooks (OnStateChange) integrate with metrics/tracing
- Race-safe state machine with generation counter

---

## From `hashicorp/go-retryablehttp`

`go-retryablehttp` is a drop-in `http.Client` replacement with retry built-in. Migration is mostly API-compatible.

### Before

```go
import "github.com/hashicorp/go-retryablehttp"

rc := retryablehttp.NewClient()
rc.RetryMax = 3
rc.RetryWaitMin = 100 * time.Millisecond
rc.RetryWaitMax = 5 * time.Second
rc.Logger = nil  // silence

// Use as drop-in:
stdClient := rc.StandardClient()
resp, err := stdClient.Get(url)
```

### After

```go
import "github.com/farhanturu/ambatukam-go"

client := ambatukam.New(ambatukam.WithRetry(ambatukam.RetryConfig{
    MaxRetries:     3,
    InitialBackoff: 100 * time.Millisecond,
    MaxBackoff:     5 * time.Second,
    Multiplier:     2.0,
}))

// Use as drop-in via RoundTrip:
stdClient := &http.Client{Transport: client.Transport()}
resp, err := stdClient.Get(url)
```

### What you gain
- Circuit breaker (not in `go-retryablehttp`)
- Bulkhead, rate limit, hooks, request ID
- Generic JSON helpers

### What you give up
- `retryablehttp.ErrorHandler` hook → use `ambatukam.Hooks.OnRetry` or custom `ShouldRetry`
- `retryablehttp.NewRequest` factory (use `http.NewRequest` directly)

---

## From `slok/goresilience`

Both libraries use a runner/middleware pattern. `goresilience` is broader (slok's has more runners) but ambatukam is more focused on HTTP.

### Field mapping
| slok/goresilience | ambatukam |
|---|---|
| `Runner` interface | `Policy` interface |
| `retry.New` | `NewRetry(RetryConfig{...})` |
| `circuitbreaker.New` | `NewCircuitBreaker(CircuitConfig{...})` |
| `bulkhead.New` | `NewBulkhead(BulkheadConfig{...})` |
| `timeout.New` | `NewTimeout(TimeoutConfig{...})` |
| `goresilience.Middleware` (composability helper) | `Chain(...)` |
| Decorator functions | Direct `WithX(...)` options |

### Before (slok)

```go
import "github.com/slok/goresilience"

runner := goresilience.RunnerChain(
    timeout.New(timeout.Config{...}),
    retry.New(retry.Config{...}),
    circuitbreaker.New(circuitbreaker.Config{...}),
)

err := runner.Run(ctx, func(ctx context.Context) error {
    // HTTP call
})
```

### After (ambatukam)

```go
import "github.com/farhanturu/ambatukam-go"

client := ambatukam.New(ambatukam.WithPolicy(ambatukam.Chain(
    ambatukam.NewTimeout(ambatukam.TimeoutConfig{...}),
    ambatukam.NewRetry(ambatukam.RetryConfig{...}),
    ambatukam.NewCircuitBreaker(ambatukam.CircuitConfig{...}),
)))

resp, err := client.Get(ctx, url)
```

### What you gain
- Direct HTTP client API (`Get`/`Post`/`Do`)
- Built-in body buffering
- JSON helpers
- More battle-tested circuit state machine (generation counter)

### What you give up
- Generic `Runner` (not tied to HTTP) — ambatukam is HTTP-only

---

## Need help with another library?

Open an issue: https://github.com/farhanturu/ambatukam-go/issues

<p align="center">
  <img src="./assets/logo-banner.svg" alt="Ambatukam Go" width="100%">
</p>

<h1 align="center">Ambatukam Go</h1>

<p align="center">
  <strong>Composable, idiomatic Go HTTP resilience.<br>
  Retry · Circuit Breaker · Bulkhead · Rate Limiter · Timeout · Hooks</strong>
</p>

<p align="center">
  <a href="https://github.com/farhanturu/ambatukam-go/actions"><img src="https://github.com/farhanturu/ambatukam-go/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/farhanturu/ambatukam-go"><img src="https://pkg.go.dev/badge/github.com/farhanturu/ambatukam-go.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/farhanturu/ambatukam-go"><img src="https://goreportcard.com/badge/github.com/farhanturu/ambatukam-go" alt="Go Report Card"></a>
  <a href="./LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License"></a>
  <a href="https://github.com/farhanturu/ambatukam-go/stargazers"><img src="https://img.shields.io/github/stars/farhanturu/ambatukam-go" alt="Stars"></a>
</p>

<p align="center">
  One library. One API. Zero dependencies. Production-grade resilience in 10 lines.
</p>

---

## Why Ambatukam Go?

Every Go backend that calls external services needs the same five things: **retry** on transient failures, **circuit breaker** to fail fast when downstream is down, **bulkhead** to limit concurrency, **rate limiting** to respect API quotas, and **per-attempt timeout** to bound latency.

Most teams stitch together 3–5 different libraries and write glue code nobody owns.

**Ambatukam Go is one library with one API.**

| Feature | Ambatukam Go | Stitched Stack |
|---|:---:|:---:|
| Retry with backoff + jitter | ✅ | `cenkalti/backoff` |
| Circuit breaker (closed/open/half-open) | ✅ | `sony/gobreaker` |
| Bulkhead (concurrency limit) | ✅ | DIY or `slok/goresilience` |
| Rate limiter (token bucket) | ✅ | `golang.org/x/time/rate` |
| Per-attempt timeout | ✅ | manual |
| Body buffering for safe POST retry | ✅ | DIY (often broken) |
| Retry-After header support | ✅ | most libs skip |
| Generic JSON helpers | ✅ | DIY |
| Request ID propagation | ✅ | DIY |
| Hooks (auth, logging, metrics) | ✅ | varies |
| Composable policies (`Chain`) | ✅ | manual |
| **Zero dependencies** | ✅ | n deps |

---

## Install

```bash
go get github.com/farhanturu/ambatukam-go
```

Requires **Go 1.21+** (uses generics, `slog`, `atomic.Int64`).

---

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/farhanturu/ambatukam-go"
)

func main() {
    client := ambatukam.New(
        ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 2 * time.Second}),
        ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 3}),
        ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{FailureThreshold: 5}),
        ambatukam.WithBulkhead(ambatukam.BulkheadConfig{MaxConcurrent: 10}),
        ambatukam.WithRateLimit(ambatukam.RateLimitConfig{Rate: 10, Burst: 5}),
    )
    defer client.Close()

    resp, err := client.Get(context.Background(), "https://api.example.com/users")
    if err != nil {
        log.Fatal(err)
    }
    defer resp.Body.Close()
    fmt.Println("status:", resp.StatusCode)
}
```

---

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│                      Client.Do()                         │
│                                                          │
│   ┌────────┐  ┌──────────┐  ┌────────┐  ┌──────────┐   │
│   │ Retry  │→│ Circuit   │→│Bulkhead│→│Rate Limit│   │
│   │        │  │ Breaker   │  │        │  │          │   │
│   └────────┘  └──────────┘  └────────┘  └──────────┘   │
│        ↓           ↓             ↓            ↓         │
│   ┌──────────────────────────────────────────────────┐  │
│   │     Timeout · Request ID · Hooks                 │  │
│   └──────────────────────────────────────────────────┘  │
│                         ↓                                │
│                   http.Client.Do()                       │
└──────────────────────────────────────────────────────────┘
```

Policies are composable middleware — outer-to-inner order: `retry → circuit → timeout → HTTP`.

---

## Features

### 🔄 Retry with Backoff

```go
ambatukam.WithRetry(ambatukam.RetryConfig{
    MaxRetries:     3,
    InitialBackoff: 100 * time.Millisecond,
    MaxBackoff:     5 * time.Second,
    Multiplier:     2.0,
    Jitter:         0.2,
})
```

Three strategies: `ExponentialBackoff`, `ConstantBackoff`, `LinearBackoff`.

Body buffering is automatic — POST bodies are read once and replayed on each retry. Only idempotent methods (GET, HEAD, PUT, DELETE, OPTIONS, TRACE) retry by default; opt in for POST with a custom `ShouldRetry`.

### ⚡ Circuit Breaker

```go
ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{
    FailureThreshold: 5,
    OpenDuration:     30 * time.Second,
    HalfOpenMaxReqs:  1,
})
```

Three-state machine: **closed → open → half-open**. Race-safe under concurrent load with atomic generation counters.

### 🚧 Bulkhead (Concurrency Limit)

```go
ambatukam.WithBulkhead(ambatukam.BulkheadConfig{
    MaxConcurrent: 10,
    MaxQueue:      100,
    QueueTimeout:  50 * time.Millisecond,
})
```

Limits in-flight requests to downstream. Optional bounded queue with timeout.

### 🚦 Rate Limiter

```go
ambatukam.WithRateLimit(ambatukam.RateLimitConfig{
    Rate:        10,                     // tokens per second
    Burst:       5,                      // bucket capacity
    WaitTimeout: 100 * time.Millisecond, // 0 = fail fast
})
```

Token bucket. `Rate <= 0` denies all requests (fail-closed).

### ⏱️ Timeout

```go
ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 2 * time.Second})
```

Per-attempt deadline. Parent `ctx` cancellation takes precedence.

### 🏷️ Request ID Propagation

```go
ambatukam.WithRequestID("X-Request-ID") // empty = default header
```

Auto-generates a 12-byte hex ID per request, or propagates an existing one.

### 🪝 Hooks

```go
ambatukam.WithHooks(ambatukam.Hooks{
    BeforeRequest: func(req *http.Request) error {
        req.Header.Set("Authorization", "Bearer "+token)
        return nil
    },
    OnRetry: func(req *http.Request, attempt int, nextDelay time.Duration) {
        log.Printf("retrying %s (attempt %d, delay %v)", req.URL, attempt, nextDelay)
    },
    OnStateChange: func(name string, from, to ambatukam.State) {
        metrics.Gauge("circuit_state").Set(string(to))
    },
})
```

Four callbacks: `BeforeRequest`, `AfterResponse`, `OnRetry`, `OnStateChange`. All optional.

### 📦 Generic JSON Helpers

```go
type User struct {
    Name string `json:"name"`
    Age  int    `json:"age"`
}

u, err := ambatukam.GetJSON[User](client, ctx, "https://api.example.com/users/1")

created, err := ambatukam.PostJSON[User](client, ctx, "https://api.example.com/users", User{Name: "bob"})
```

Auto-handles JSON encode/decode, content-type, and 4xx/5xx errors as `RequestError`.

---

## Preset Configs

Ready-to-use configurations for common scenarios:

```go
// Balanced production defaults
client := ambatukam.New(ambatukam.ProductionConfig()...)

// Strict, fast-fail for fragile downstreams
client := ambatukam.New(ambatukam.AggressiveConfig()...)

// Generous config for critical services
client := ambatukam.New(ambatukam.ConservativeConfig()...)
```

| Preset | Retries | Timeout | Circuit Threshold | Bulkhead |
|--------|---------|---------|-------------------|----------|
| **Production** | 3 | 30s | 5 failures | NumCPU×4 |
| **Aggressive** | 1 | 5s | 3 failures | NumCPU×2 |
| **Conservative** | 5 | 60s | 20 failures | NumCPU×8 |

---

## Patterns

### Stripe / Payment Gateway

```go
client := ambatukam.New(
    ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 10 * time.Second}),
    ambatukam.WithRetry(ambatukam.RetryConfig{
        MaxRetries: 3,
        Backoff:    ambatukam.ConstantBackoff(500 * time.Millisecond),
    }),
    ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{FailureThreshold: 5}),
)
```

### Microservice with Auth + Tracing

```go
client := ambatukam.New(
    ambatukam.WithRequestID("X-Request-ID"),
    ambatukam.WithHooks(ambatukam.Hooks{
        BeforeRequest: func(req *http.Request) error {
            req.Header.Set("Authorization", "Bearer "+getToken())
            return nil
        },
    }),
    ambatukam.WithRetry(ambatukam.DefaultRetryConfig()),
)
```

### Third-Party API with Rate Limit

```go
client := ambatukam.New(
    ambatukam.WithRateLimit(ambatukam.RateLimitConfig{
        Rate:        5,
        Burst:       10,
        WaitTimeout: 2 * time.Second,
    }),
    ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 30 * time.Second}),
)
```

### Custom Composition Order

```go
client := ambatukam.New(ambatukam.WithPolicy(ambatukam.Chain(
    ambatukam.NewRetry(ambatukam.DefaultRetryConfig()),
    ambatukam.NewCircuitBreaker(ambatukam.DefaultCircuitConfig()),
    ambatukam.NewTimeout(ambatukam.TimeoutConfig{Timeout: 5 * time.Second}),
)))
```

**Order matters**: outer-to-inner is `[retry [circuit [timeout [HTTP]]]]`.

---

## Error Handling

Use `errors.Is` to distinguish error types:

```go
resp, err := client.Get(ctx, url)
switch {
case errors.Is(err, ambatukam.ErrCircuitOpen):    // downstream is down
case errors.Is(err, ambatukam.ErrMaxRetries):     // gave up after N attempts
case errors.Is(err, ambatukam.ErrTimeout):        // attempt hit its deadline
case errors.Is(err, ambatukam.ErrBulkheadFull):   // at concurrency cap
case errors.Is(err, ambatukam.ErrRateLimited):    // rate-limited
case errors.Is(err, ambatukam.ErrNilRequest):     // programming error
case errors.Is(err, context.Canceled):            // ctx was canceled
}
```

For full context (method, URL, status, attempts):

```go
var reqErr *ambatukam.RequestError
if errors.As(err, &reqErr) {
    log.Printf("%s %s returned %d after %d attempts",
        reqErr.Method, reqErr.URL, reqErr.Status, reqErr.Attempts)
}
```

Mark errors as non-retryable:

```go
resp, err := client.Get(ctx, url)
if err != nil {
    return ambatukam.Permanent(err) // skip retry
}
```

---

## Benchmarks

Measured on Intel Core i5-8250U @ 1.60GHz, Linux, Go 1.21 (`go test -bench=. -benchmem -benchtime=2s`).

| Setup | ns/op | B/op | allocs/op |
|---|---|---|---|
| `http.Client` (raw stdlib) | 98,785 | 5,106 | 63 |
| Ambatukam Go (no policies) | 98,325 | 4,466 | 57 |
| Ambatukam Go (retry=3) | 96,879 | 4,505 | 58 |
| Ambatukam Go (full stack) | 235,341 | 16,757 | 120 |
| Ambatukam Go (parallel) | 26,064 | 8,214 | 77 |

Run locally: `go test -bench=. -benchmem -benchtime=2s ./...`

---

## Migration

<details>
<summary><strong>From cenkalti/backoff + sony/gobreaker</strong></summary>

```go
// Before: two libraries, manual wiring
import (
    "github.com/cenkalti/backoff/v4"
    "github.com/sony/gobreaker"
)

// After: one library, one config
import "github.com/farhanturu/ambatukam-go"

client := ambatukam.New(
    ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 3}),
    ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{FailureThreshold: 5}),
)
```
</details>

<details>
<summary><strong>From hashicorp/go-retryablehttp</strong></summary>

Ambatukam Go's `*Client` is a drop-in `*http.Client`. Wrap your existing transport via `WithHTTPClient`, or use `Do`/`Get`/`Post` directly. Adds circuit breaker, bulkhead, rate limit, hooks, and request ID.
</details>

<details>
<summary><strong>From slok/goresilience</strong></summary>

Both use a runner/middleware pattern. See [MIGRATION.md](./docs/MIGRATION.md) for a detailed guide.
</details>

---

## Documentation

| Document | Description |
|----------|-------------|
| [README.md](./README.md) | You are here |
| [COOKBOOK.md](./docs/COOKBOOK.md) | Recipes for common patterns |
| [FAQ.md](./docs/FAQ.md) | Frequently asked questions |
| [MIGRATION.md](./docs/MIGRATION.md) | Migrating from other libraries |
| [CONTRIBUTING.md](./CONTRIBUTING.md) | How to contribute |
| [SECURITY.md](./SECURITY.md) | Security policy |

---

## Troubleshooting

| Problem | Solution |
|---------|----------|
| POST isn't being retried | Only idempotent methods retry by default. Use custom `ShouldRetry` or idempotency-key header. |
| Circuit opens too often | Lower `FailureThreshold` or increase `OpenDuration`. Use `OnStateChange` hook to monitor. |
| Bulkhead denies immediately | Increase `MaxQueue` or `MaxConcurrent`. See [COOKBOOK](./docs/COOKBOOK.md). |
| Rate limit denies unexpectedly | `Rate == 0` = disabled, `Rate < 0` = deny all. Verify your config value. |
| Need debug logging | Use `ambatukam.WithDebug()` for verbose logging. |

---

## Roadmap

### v1.0 (current)
Retry, circuit breaker, bulkhead, rate limiter, timeout, request ID, hooks, generic JSON helpers, permanent errors, preset configs.

### v1.1 (next)
Hedged requests (parallel speculative retries), fallback strategy (return stale data on failure), request deduplication (singleflight).

### v2.0
OpenTelemetry tracing + Prometheus metrics, adaptive timeout (based on p99 latency), distributed (Redis-backed) circuit breaker, gRPC support.

---

## Contributing

PRs welcome. Run `go test ./...` and `go vet ./...` before submitting; add a test for any new behavior. See [CONTRIBUTING.md](./CONTRIBUTING.md).

---

## License

MIT — see [LICENSE](./LICENSE).

---

<p align="center">
  <img src="./assets/logo-icon.svg" alt="Ambatukam Go" width="64">
  <br>
  <sub>Built with ❤️ by <a href="https://github.com/farhanturu">farhanturu</a></sub>
</p>

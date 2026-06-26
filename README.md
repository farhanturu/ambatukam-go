<p align="center">
  <img src="./assets/logo-banner.svg" alt="Ambatukam Go" width="100%">
</p>

<h1 align="center">Ambatukam Go</h1>

<p align="center">
  <strong>Your Go HTTP client just got superpowers.<br>
  One line of code. Zero dependencies. Production-grade resilience.</strong>
</p>

<p align="center">
  <a href="https://github.com/farhanturu/ambatukam-go/actions"><img src="https://github.com/farhanturu/ambatukam-go/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/farhanturu/ambatukam-go"><img src="https://pkg.go.dev/badge/github.com/farhanturu/ambatukam-go.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/farhanturu/ambatukam-go"><img src="https://goreportcard.com/badge/github.com/farhanturu/ambatukam-go" alt="Go Report Card"></a>
  <a href="./LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License"></a>
  <a href="https://github.com/farhanturu/ambatukam-go/stargazers"><img src="https://img.shields.io/github/stars/farhanturu/ambatukam-go" alt="Stars"></a>
</p>

<p align="center">
  Stop writing retry loops. Stop handling timeouts manually.<br>
  Stop panicking when Stripe goes down at 3 AM.<br>
  <strong>Ambatukam Go handles all of it — automatically.</strong>
</p>

---

## 😤 The Problem

Every Go backend that calls external services ends up like this:

```go
// Your code at 3 AM when Stripe is down
resp, err := http.Get("https://api.stripe.com/charges")
if err != nil {
    // retry? how many times? with what backoff?
    // what if it keeps failing? circuit break?
    // what about rate limits? timeouts?
    // TODO: fix this later (you never will)
}
```

You end up stitching together 3-5 libraries, writing glue code nobody owns, and debugging resilience bugs at 3 AM.

## 😎 The Solution

```go
// Your code with Ambatukam Go
client := ambatukam.New(
    ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 3}),
    ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{FailureThreshold: 5}),
    ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 2 * time.Second}),
)
resp, err := client.Get(ctx, "https://api.stripe.com/charges")
// ✅ Auto-retry with exponential backoff
// ✅ Circuit opens when Stripe is down
// ✅ Timeout per attempt
// ✅ You sleep at 3 AM
```

**10 lines. Zero dependencies. Production-grade.**

---

## ⚡ Why Ambatukam Go?

| Feature | Ambatukam Go | Other Libraries |
|---|:---:|:---:|
| Retry with backoff + jitter | ✅ Built-in | Need `cenkalti/backoff` |
| Circuit breaker | ✅ Built-in | Need `sony/gobreaker` |
| Bulkhead (concurrency limit) | ✅ Built-in | DIY or `slok/goresilience` |
| Rate limiter | ✅ Built-in | Need `golang.org/x/time/rate` |
| Per-attempt timeout | ✅ Built-in | Manual |
| Fallback strategy | ✅ Built-in | DIY |
| Singleflight (dedup) | ✅ Built-in | Need `golang.org/x/sync` |
| Health check endpoint | ✅ Built-in | DIY |
| Metrics interface | ✅ Built-in | DIY |
| Body buffering for POST retry | ✅ Automatic | Often broken |
| Retry-After header | ✅ Automatic | Most skip |
| Generic JSON helpers | ✅ Built-in | DIY |
| Request ID propagation | ✅ Built-in | DIY |
| Hooks (auth, logging, metrics) | ✅ 4 callbacks | Varies |
| Composable policies | ✅ `Chain()` | Manual |
| **Zero dependencies** | ✅ **None** | 3-5 deps |

---

## 🚀 Install

```bash
go get github.com/farhanturu/ambatukam-go
```

Requires **Go 1.21+**. Zero external dependencies.

---

## 🎯 Quick Start

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

## 🏗️ Architecture

```
┌──────────────────────────────────────────────────────────┐
│                      Client.Do()                         │
│                                                          │
│   ┌────────┐  ┌──────────┐  ┌────────┐  ┌──────────┐   │
│   │ Retry  │→│ Circuit   │→│Bulkhead│→│Rate Limit│   │
│   │        │  │ Breaker   │  │        │  │          │   │
│   └────────┘  └──────────┘  └────────┘  └──────────┘   │
│        ↓           ↓             ↓            ↓         │
│   ┌────────┐  ┌──────────┐  ┌──────────┐               │
│   │Fallback│  │Singleflight│ │  Timeout │               │
│   └────────┘  └──────────┘  └──────────┘               │
│        ↓           ↓             ↓                      │
│   ┌──────────────────────────────────────────────────┐  │
│   │     Request ID · Hooks · Metrics                 │  │
│   └──────────────────────────────────────────────────┘  │
│                         ↓                                │
│                   http.Client.Do()                       │
└──────────────────────────────────────────────────────────┘
```

Policies are composable middleware — outer-to-inner order: `retry → circuit → fallback → HTTP`.

---

## 🎨 Features

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

Body buffering is automatic — POST bodies are read once and replayed on each retry. Only idempotent methods retry by default; opt in for POST with custom `ShouldRetry`.

### ⚡ Circuit Breaker

```go
ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{
    FailureThreshold: 5,
    OpenDuration:     30 * time.Second,
    HalfOpenMaxReqs:  1,
})
```

Three-state machine: **closed → open → half-open**. Race-safe with `sync.RWMutex` for better read concurrency.

### 🚧 Bulkhead

```go
ambatukam.WithBulkhead(ambatukam.BulkheadConfig{
    MaxConcurrent: 10,
    MaxQueue:      100,
    QueueTimeout:  50 * time.Millisecond,
})
```

Limits in-flight requests. Optional bounded queue with timeout.

### 🚦 Rate Limiter

```go
ambatukam.WithRateLimit(ambatukam.RateLimitConfig{
    Rate:        10,
    Burst:       5,
    WaitTimeout: 100 * time.Millisecond,
})
```

Token bucket. `Rate <= 0` denies all requests.

### ⏱️ Timeout

```go
ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 2 * time.Second})
```

Per-attempt deadline. Parent `ctx` cancellation takes precedence.

### 🛟 Fallback

```go
ambatukam.WithFallback(ambatukam.FallbackConfig{
    Handler: func(req *http.Request, err error) (*http.Response, error) {
        return cachedResponse, nil
    },
})
```

Return custom response when everything fails. Never leave your users hanging.

### 🔗 Singleflight

```go
ambatukam.WithSingleflight()
```

Deduplicate identical concurrent requests. 10 goroutines requesting the same data = 1 HTTP call.

### 🏷️ Request ID

```go
ambatukam.WithRequestID("X-Request-ID")
```

Auto-generates 12-byte hex ID per request, or propagates existing one.

### 🪝 Hooks

```go
ambatukam.WithHooks(ambatukam.Hooks{
    BeforeRequest: func(req *http.Request) error {
        req.Header.Set("Authorization", "Bearer "+token)
        return nil
    },
    OnRetry: func(req *http.Request, attempt int, nextDelay time.Duration) {
        log.Printf("retrying %s (attempt %d)", req.URL, attempt)
    },
    OnStateChange: func(name string, from, to ambatukam.State) {
        metrics.Gauge("circuit_state").Set(string(to))
    },
    OnFallback: func(req *http.Request, err error) {
        log.Printf("fallback triggered: %v", err)
    },
})
```

Five callbacks: `BeforeRequest`, `AfterResponse`, `OnRetry`, `OnStateChange`, `OnFallback`.

### 📊 Metrics

```go
ambatukam.WithMetrics(myPrometheusRecorder)
```

Implement `MetricsRecorder` interface for Prometheus, Datadog, or any metrics system.

### 🏥 Health Check

```go
hc := client.HealthChecker()
http.Handle("/health", hc.Handler())
```

Returns JSON with policy status, memory stats, and uptime.

```json
{
  "status": "healthy",
  "timestamp": "2026-06-26T20:00:00Z",
  "uptime": "5m30s",
  "policies": {
    "circuit_breaker": "closed",
    "bulkhead_in_flight": "3",
    "bulkhead_denied": "0"
  },
  "memory": {
    "alloc_bytes": 1234567,
    "num_gc": 5
  }
}
```

### 📦 Generic JSON Helpers

```go
u, err := ambatukam.GetJSON[User](client, ctx, "https://api.example.com/users/1")
created, err := ambatukam.PostJSON[User](client, ctx, "https://api.example.com/users", user)
```

Auto-handles JSON encode/decode, content-type, and 4xx/5xx errors.

---

## 🎯 Preset Configs

Don't want to tune? Use presets:

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

## 💡 Real-World Patterns

### Stripe / Payment Gateway

```go
client := ambatukam.New(
    ambatukam.WithTimeout(ambatukam.TimeoutConfig{Timeout: 10 * time.Second}),
    ambatukam.WithRetry(ambatukam.RetryConfig{
        MaxRetries: 3,
        Backoff:    ambatukam.ConstantBackoff(500 * time.Millisecond),
    }),
    ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{FailureThreshold: 5}),
    ambatukam.WithFallback(ambatukam.FallbackConfig{
        Handler: func(req *http.Request, err error) (*http.Response, error) {
            return nil, errors.New("payment service unavailable, please retry later")
        },
    }),
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
    ambatukam.WithSingleflight(),
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
    ambatukam.WithFallback(ambatukam.FallbackConfig{
        Handler: func(req *http.Request, err error) (*http.Response, error) {
            return getCachedData(req.URL.String())
        },
    }),
)
```

---

## 🚨 Error Handling

```go
resp, err := client.Get(ctx, url)
switch {
case errors.Is(err, ambatukam.ErrCircuitOpen):    // downstream is down
case errors.Is(err, ambatukam.ErrMaxRetries):     // gave up after N attempts
case errors.Is(err, ambatukam.ErrTimeout):        // attempt hit its deadline
case errors.Is(err, ambatukam.ErrBulkheadFull):   // at concurrency cap
case errors.Is(err, ambatukam.ErrRateLimited):    // rate-limited
case errors.Is(err, ambatukam.ErrFallback):       // fallback failed
case errors.Is(err, ambatukam.ErrNilRequest):     // programming error
}
```

For full context:

```go
var reqErr *ambatukam.RequestError
if errors.As(err, &reqErr) {
    log.Printf("[%s] %s %s returned %d after %d attempts",
        reqErr.Policy, reqErr.Method, reqErr.URL, reqErr.Status, reqErr.Attempts)
}
```

---

## 📈 Benchmarks

```
go test -bench=. -benchmem -benchtime=2s ./...
```

| Setup | ns/op | B/op | allocs/op |
|---|---|---|---|
| `http.Client` (raw stdlib) | 98,785 | 5,106 | 63 |
| Ambatukam Go (no policies) | 98,325 | 4,466 | 57 |
| Ambatukam Go (retry=3) | 96,879 | 4,505 | 58 |
| Ambatukam Go (full stack) | 235,341 | 16,757 | 120 |
| Ambatukam Go (parallel) | 26,064 | 8,214 | 77 |

**Zero overhead** when no policies enabled. Full stack costs ~2.4x vs raw stdlib.

---

## 🔄 Migration

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

Ambatukam Go's `*Client` is a drop-in `*http.Client`. Wrap your existing transport via `WithHTTPClient`, or use `Do`/`Get`/`Post` directly. Adds circuit breaker, bulkhead, rate limit, fallback, singleflight, hooks, and request ID.
</details>

---

## 📚 Documentation

| Document | Description |
|----------|-------------|
| [README.md](./README.md) | You are here |
| [COOKBOOK.md](./docs/COOKBOOK.md) | Recipes for common patterns |
| [FAQ.md](./docs/FAQ.md) | Frequently asked questions |
| [MIGRATION.md](./docs/MIGRATION.md) | Migrating from other libraries |
| [CONTRIBUTING.md](./CONTRIBUTING.md) | How to contribute |
| [SECURITY.md](./SECURITY.md) | Security policy |

---

## 🐛 Troubleshooting

| Problem | Solution |
|---------|----------|
| POST isn't being retried | Only idempotent methods retry by default. Use custom `ShouldRetry`. |
| Circuit opens too often | Lower `FailureThreshold` or increase `OpenDuration`. |
| Bulkhead denies immediately | Increase `MaxQueue` or `MaxConcurrent`. |
| Rate limit denies unexpectedly | `Rate == 0` = disabled, `Rate < 0` = deny all. |
| Need debug logging | Use `ambatukam.WithDebug()`. |

---

## 🗺️ Roadmap

### v1.1 (current)
Retry, circuit breaker, bulkhead, rate limiter, timeout, request ID, hooks, generic JSON helpers, permanent errors, preset configs, **fallback strategy, singleflight, health check, metrics interface**.

### v2.0 (next)
OpenTelemetry tracing + Prometheus metrics, adaptive timeout (based on p99 latency), distributed (Redis-backed) circuit breaker, gRPC support.

---

## 🤝 Contributing

PRs welcome. Run `go test ./...` and `go vet ./...` before submitting; add a test for any new behavior. See [CONTRIBUTING.md](./CONTRIBUTING.md).

---

## 📄 License

MIT — see [LICENSE](./LICENSE).

---

<p align="center">
  <img src="./assets/logo-icon.svg" alt="Ambatukam Go" width="64">
  <br>
  <strong>Stop writing resilience code. Start shipping features.</strong>
  <br><br>
  <sub>Built with ❤️ by <a href="https://github.com/farhanturu">farhanturu</a></sub>
</p>

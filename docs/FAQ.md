# FAQ

## Why isn't my POST being retried?

By default, only idempotent methods are retried (GET, HEAD, PUT, DELETE, OPTIONS, TRACE).
POST is non-idempotent — retrying a POST that succeeded server-side but lost the response could
cause double-charge (e.g., double-charging a credit card).

To opt in to retrying POST, provide a custom `ShouldRetry`:

```go
client := ambatukam.New(ambatukam.WithRetry(ambatukam.RetryConfig{
    MaxRetries: 3,
    ShouldRetry: func(resp *http.Response, err error) bool {
        // Only retry on 5xx. Add Idempotency-Key header per request.
        return err != nil || (resp != nil && resp.StatusCode >= 500)
    },
}))
```

## Why does the circuit stay open?

The breaker stays open for `OpenDuration` (default 30s) before allowing a half-open trial.
If the trial fails, it goes back to open for another `OpenDuration`.

To check state programmatically: use `circuit.State()` (if exposed) or hook `OnStateChange`.

## Why is overhead non-zero even with no policies?

Even an empty `Client` has minimal overhead from:
- Policy chain traversal (slice of function pointers)
- Body buffering setup (cheap if `req.Body == nil`)
- Context wrapping in the timeout helper

Benchmarks show ~0% overhead for `goresilience (no policies)` vs raw `http.Client`. See the
Benchmarks section in the README.

## How do I see what policies are doing?

Use `WithDebug()` to enable DEBUG-level slog output:

```go
client := ambatukam.New(ambatukam.WithDebug())
```

Or use `WithLogger` with your own structured logger:

```go
client := ambatukam.New(ambatukam.WithLogger(slog.Default()))
```

For per-event observability, use hooks (`OnRetry`, `OnStateChange`).

## How do I test resilience behavior?

See the [Cookbook §8](./COOKBOOK.md#8-test-resilience-behavior) for the flaky-server pattern.

## Can I use this with gRPC?

Not directly — ambatukam is HTTP-only. For gRPC, see:
- [`grpc-go`](https://github.com/grpc/grpc-go) has its own interceptor system
- Or wrap a gRPC call in `client.Do` with a custom transport (complex, not recommended)

## Does this work with `*http.Client` from third-party libraries?

Yes. Wrap ambatukam as a Transport:

```go
amba := ambatukam.New(...)
thirdPartyClient := &http.Client{Transport: amba.Transport()}
```

Or use `client.Transport()` directly.

## What's the difference between `Chain` and `WithX` options?

`WithX` options build a chain in the order they're declared. `Chain(...)` is for manual composition
when you need policies not exposed as options, or custom ordering.

```go
// These are equivalent for the common case:
client := ambatukam.New(
    ambatukam.WithRetry(...),
    ambatukam.WithCircuitBreaker(...),
)

client := ambatukam.New(ambatukam.WithPolicy(ambatukam.Chain(
    ambatukam.NewRetry(...),
    ambatukam.NewCircuitBreaker(...),
)))
```

## What about retries during graceful shutdown?

If your server is shutting down, the `context.Context` propagated via `Get`/`Post`/`Do` is the
shutdown signal. Cancel it from the caller side; ambatukam respects context cancellation and
will not retry after ctx is done.

## Does ambatukam deduplicate concurrent identical requests?

Not in v1.1. Use singleflight from `golang.org/x/sync/singleflight` for that pattern; wrap it
as a custom policy or wrap the call site.

## Can I run a custom policy alongside the built-in ones?

Yes. Implement the `Policy` interface and use `WithPolicy(myPolicy)`:

```go
type myPolicy struct{}
func (p *myPolicy) Execute(ctx context.Context, req *http.Request, next ambatukam.PolicyFunc) (*http.Response, error) {
    // ... your logic ...
    return next(ctx, req)
}

client := ambatukam.New(
    ambatukam.WithRetry(...),
    ambatukam.WithPolicy(&myPolicy{}),
)
```

## How do I migrate from another library?

See [MIGRATION.md](./MIGRATION.md) for step-by-step guides from `cenkalti/backoff`,
`sony/gobreaker`, `hashicorp/go-retryablehttp`, and `slok/goresilience`.

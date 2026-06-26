# Cookbook

Common patterns for using Ambatukam Go.

---

## 1. Retry only on 5xx (default behavior; this is how to be explicit)

```go
client := ambatukam.New(ambatukam.WithRetry(ambatukam.RetryConfig{
    MaxRetries: 3,
    ShouldRetry: func(resp *http.Response, err error) bool {
        return err != nil || (resp != nil && resp.StatusCode >= 500)
    },
}))
```

## 2. Retry POST with idempotency-key

```go
client := ambatukam.New(ambatukam.WithRetry(ambatukam.RetryConfig{
    MaxRetries: 3,
    ShouldRetry: func(resp *http.Response, err error) bool {
        // Only retry on 5xx or network errors; never on 4xx (client error).
        return err != nil || (resp != nil && resp.StatusCode >= 500)
    },
}))

// Add idempotency-key header per request:
req, _ := http.NewRequest("POST", url, body)
req.Header.Set("Idempotency-Key", generateUUID())
resp, err := client.Do(req)
```

## 3. Circuit breaker per host

When calling multiple services, give each its own breaker:

```go
func clientForHost(host string) *ambatukam.Client {
    return ambatukam.New(
        ambatukam.WithCircuitBreaker(ambatukam.CircuitConfig{
            FailureThreshold: 5,
            OpenDuration:     30 * time.Second,
        }),
    )
}

stripe := clientForHost("api.stripe.com")
sendgrid := clientForHost("api.sendgrid.com")
```

(The breaker state is per-`Client` instance; separate clients = separate breakers.)

## 4. Inject auth header on every request

```go
client := ambatukam.New(ambatukam.WithHooks(ambatukam.Hooks{
    BeforeRequest: func(req *http.Request) error {
        req.Header.Set("Authorization", "Bearer "+getToken())
        return nil
    },
}))
```

`BeforeRequest` runs on EVERY attempt (including retries), so the token is refreshed automatically.

## 5. Rate limit per user (custom)

This library's built-in rate limiter is global per-Client. For per-user limiting, use a custom policy:

```go
type perUserLimiter struct {
    ambatukam.Policy
    mu       sync.Mutex
    limiters map[string]*ambatukam.RateLimitPolicy
    rate     float64
    burst    uint32
}

func (p *perUserLimiter) Execute(ctx context.Context, req *http.Request, next ambatukam.PolicyFunc) (*http.Response, error) {
    userID := req.Header.Get("X-User-ID")
    p.mu.Lock()
    rl, ok := p.limiters[userID]
    if !ok {
        rl = ambatukam.NewRateLimit(ambatukam.RateLimitConfig{Rate: p.rate, Burst: p.burst})
        p.limiters[userID] = rl
    }
    p.mu.Unlock()
    return rl.Execute(ctx, req, next)
}
```

## 6. Use with `*http.Client` from another library

If a third-party library accepts only `*http.Client`, wrap ambatukam as a Transport:

```go
amba := ambatukam.New(ambatukam.WithRetry(...), ambatukam.WithCircuitBreaker(...))
httpClient := &http.Client{Transport: amba.Transport()}

// Now use httpClient with oauth2, OpenAPI generators, etc.
tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "..."})
oauthClient := oauth2.NewClient(context.Background(), tokenSource)
oauthClient.Transport = amba.Transport()  // wraps the OAuth client's transport
```

## 7. Distributed tracing integration (without OTel dependency)

If you don't want the OTel dependency but use Datadog/New Relic/etc., use hooks:

```go
client := ambatukam.New(ambatukam.WithHooks(ambatukam.Hooks{
    BeforeRequest: func(req *http.Request) error {
        span, _ := tracer.StartSpanFromContext(req.Context(), "http.client")
        *req = *req.WithContext(tracer.ContextWithSpan(req.Context(), span))
        return nil
    },
    AfterResponse: func(req *http.Request, resp *http.Response, err error) {
        span, _ := tracer.SpanFromContext(req.Context())
        span.Finish(tracer.WithError(err))
    },
}))
```

## 8. Test resilience behavior

Use a flaky `httptest.Server`:

```go
var hits atomic.Int32
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if hits.Add(1) <= 2 {
        w.WriteHeader(http.StatusInternalServerError)
        return
    }
    w.WriteHeader(http.StatusOK)
}))
defer srv.Close()

client := ambatukam.New(ambatukam.WithRetry(ambatukam.RetryConfig{
    MaxRetries: 3,
    Backoff: ambatukam.ConstantBackoff(5 * time.Millisecond),
}))

resp, err := client.Get(ctx, srv.URL)
// err is nil; resp.StatusCode == 200; hits.Load() == 3
```

## 9. Distributed logging of retries

```go
client := ambatukam.New(
    ambatukam.WithRetry(ambatukam.RetryConfig{MaxRetries: 3}),
    ambatukam.WithDebug(),  // logs all retries to stderr at DEBUG level
)
```

Or use `WithLogger` to integrate with your own structured logger.

package ambatukam

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// State is the publicly-visible state of a circuit breaker.
type State string

const (
	StateClosed   State = "closed"
	StateOpen     State = "open"
	StateHalfOpen State = "half-open"
)

// CircuitBreakerPolicy is a circuit breaker resilience policy.
//
// State machine:
//   - Closed:    consecutive failures are counted via cb.failures. When the
//     counter reaches cfg.FailureThreshold, the breaker transitions
//     to Open and records the time the open window began.
//   - Open:      every request fails fast with ErrCircuitOpen. When the open
//     window has elapsed (cfg.OpenDuration), the next request
//     triggers the Open->HalfOpen transition and is admitted as
//     a trial.
//   - HalfOpen:  up to cfg.HalfOpenMaxReqs trial requests are admitted. A
//     successful trial closes the breaker. A failed trial re-opens
//     the breaker.
//
// Concurrency:
//   - cb.mu protects state, openedAt, halfOpenPermits, halfOpenInFlight.
//     It is never held across next(ctx, req).
//   - cb.failures is read atomically (fast Closed hot path) and reset to 0
//     atomically on every success in Closed state and on the HalfOpen->Closed
//     transition.
//   - cb.generation is a monotonic counter incremented on every state
//     transition. A request entering HalfOpen records the generation; on
//     response the result is only applied if the breaker is still on the
//     same generation. Otherwise the breaker has moved on and the trial is
//     discarded (no stale writes).
type CircuitBreakerPolicy struct {
	cfg    CircuitConfig
	logger *slog.Logger
	hooks  Hooks
	name   string

	mu               sync.Mutex
	state            State
	openedAt         time.Time
	halfOpenPermits  uint32
	halfOpenInFlight uint32

	failures   atomic.Uint32
	generation atomic.Uint64
}

// DefaultCircuitConfig returns the circuit breaker configuration used when
// none is provided. Callers can extend these defaults programmatically.
func DefaultCircuitConfig() CircuitConfig {
	return CircuitConfig{
		FailureThreshold: 5,
		OpenDuration:     30 * time.Second,
		HalfOpenMaxReqs:  1,
	}
}

// NewCircuitBreaker constructs a CircuitBreakerPolicy. Zero-valued config
// fields are filled with the package defaults via DefaultCircuitConfig:
// FailureThreshold=5, OpenDuration=30s, HalfOpenMaxReqs=1.
func NewCircuitBreaker(cfg CircuitConfig) *CircuitBreakerPolicy {
	def := DefaultCircuitConfig()
	if cfg.FailureThreshold == 0 {
		cfg.FailureThreshold = def.FailureThreshold
	}
	if cfg.OpenDuration == 0 {
		cfg.OpenDuration = def.OpenDuration
	}
	if cfg.HalfOpenMaxReqs == 0 {
		cfg.HalfOpenMaxReqs = def.HalfOpenMaxReqs
	}
	return &CircuitBreakerPolicy{
		cfg:    cfg,
		logger: slog.Default(),
		state:  StateClosed,
		name:   "default",
	}
}

// WithLogger sets a non-nil logger on the policy.
func (cb *CircuitBreakerPolicy) WithLogger(l *slog.Logger) *CircuitBreakerPolicy {
	if l != nil {
		cb.logger = l
	}
	return cb
}

// WithHooks installs user callbacks fired on circuit state changes. Only
// OnStateChange is used by this policy.
func (cb *CircuitBreakerPolicy) WithHooks(h Hooks) *CircuitBreakerPolicy {
	cb.hooks = h
	return cb
}

// WithName sets the breaker identifier reported in OnStateChange callbacks.
// An empty name leaves the existing name unchanged (default: "default").
func (cb *CircuitBreakerPolicy) WithName(name string) *CircuitBreakerPolicy {
	if name != "" {
		cb.name = name
	}
	return cb
}

// State returns the current breaker state. Safe to call concurrently.
func (cb *CircuitBreakerPolicy) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// shouldTrip decides whether the result of a request should be treated as a
// failure for the breaker. Default behaviour: an error or a 5xx response
// trips the breaker; 4xx responses do not.
func (cb *CircuitBreakerPolicy) shouldTrip(resp *http.Response, err error) bool {
	if cb.cfg.ShouldTrip != nil {
		return cb.cfg.ShouldTrip(resp, err)
	}
	if err != nil {
		return true
	}
	if resp != nil && resp.StatusCode >= 500 {
		return true
	}
	return false
}

// allowRequest returns (allowed, genAtEntry). On Open->HalfOpen transition
// the new generation is recorded so that the first probe after the open
// window is recognised as a half-open trial, not a stale closed/open probe.
// If the Open->HalfOpen transition fires here, the OnStateChange hook is
// invoked AFTER the mutex is released (so user code cannot deadlock by
// calling back into the library).
func (cb *CircuitBreakerPolicy) allowRequest() (bool, uint64) {
	var (
		transitionedFrom State
		transitionedTo   State
		didTransition    bool
		allow            bool
		gen              uint64
	)
	cb.mu.Lock()
	switch cb.state {
	case StateClosed:
		allow = true
		gen = cb.generation.Load()
		cb.mu.Unlock()
		return allow, gen
	case StateOpen:
		if time.Since(cb.openedAt) >= cb.cfg.OpenDuration {
			cb.state = StateHalfOpen
			cb.halfOpenPermits = cb.cfg.HalfOpenMaxReqs
			cb.halfOpenInFlight = 0
			cb.generation.Add(1)
			cb.logger.Info("circuit: open -> half-open",
				slog.Uint64("permits", uint64(cb.halfOpenPermits)),
			)
			transitionedFrom = StateOpen
			transitionedTo = StateHalfOpen
			didTransition = true
			// fall through to admit this request as a trial
		} else {
			gen = cb.generation.Load()
			cb.mu.Unlock()
			return false, gen
		}
		fallthrough
	case StateHalfOpen:
		if cb.halfOpenPermits > 0 {
			cb.halfOpenPermits--
			cb.halfOpenInFlight++
			allow = true
		}
		gen = cb.generation.Load()
	}
	cb.mu.Unlock()
	if didTransition && cb.hooks.OnStateChange != nil {
		cb.hooks.OnStateChange(cb.name, transitionedFrom, transitionedTo)
	}
	return allow, gen
}

// onSuccess applies a successful result. Stale trials (generation has moved)
// are dropped. The OnStateChange hook is fired AFTER the mutex is released.
func (cb *CircuitBreakerPolicy) onSuccess(genAtEntry uint64) {
	var (
		transitionedFrom State
		transitionedTo   State
		didTransition    bool
	)
	cb.mu.Lock()
	if cb.generation.Load() == genAtEntry {
		switch cb.state {
		case StateClosed:
			cb.failures.Store(0)
		case StateHalfOpen:
			cb.halfOpenInFlight--
			cb.state = StateClosed
			cb.failures.Store(0)
			cb.openedAt = time.Time{}
			cb.halfOpenPermits = 0
			cb.halfOpenInFlight = 0
			cb.generation.Add(1)
			cb.logger.Info("circuit: half-open -> closed")
			transitionedFrom = StateHalfOpen
			transitionedTo = StateClosed
			didTransition = true
		}
	}
	cb.mu.Unlock()
	if didTransition && cb.hooks.OnStateChange != nil {
		cb.hooks.OnStateChange(cb.name, transitionedFrom, transitionedTo)
	}
}

// onFailure applies a failed result. Stale trials (generation has moved) are
// dropped. The OnStateChange hook is fired AFTER the mutex is released.
func (cb *CircuitBreakerPolicy) onFailure(genAtEntry uint64) {
	var (
		transitionedFrom State
		transitionedTo   State
		didTransition    bool
	)
	cb.mu.Lock()
	if cb.generation.Load() == genAtEntry {
		switch cb.state {
		case StateClosed:
			n := cb.failures.Add(1)
			if n >= cb.cfg.FailureThreshold {
				cb.state = StateOpen
				cb.openedAt = time.Now()
				cb.halfOpenPermits = 0
				cb.halfOpenInFlight = 0
				cb.generation.Add(1)
				cb.logger.Warn("circuit: closed -> open",
					slog.Uint64("failures", uint64(n)),
					slog.Duration("open_duration", cb.cfg.OpenDuration),
				)
				transitionedFrom = StateClosed
				transitionedTo = StateOpen
				didTransition = true
			}
		case StateHalfOpen:
			cb.halfOpenInFlight--
			cb.state = StateOpen
			cb.openedAt = time.Now()
			cb.halfOpenPermits = 0
			cb.halfOpenInFlight = 0
			cb.generation.Add(1)
			cb.logger.Warn("circuit: half-open -> open (trial failed)")
			transitionedFrom = StateHalfOpen
			transitionedTo = StateOpen
			didTransition = true
		}
	}
	cb.mu.Unlock()
	if didTransition && cb.hooks.OnStateChange != nil {
		cb.hooks.OnStateChange(cb.name, transitionedFrom, transitionedTo)
	}
}

// Execute runs a single request through the breaker. A request denied by the
// breaker returns (nil, ErrCircuitOpen) without invoking next. Admitted
// requests call next and the result is routed through shouldTrip/onSuccess/
// onFailure.
func (cb *CircuitBreakerPolicy) Execute(ctx context.Context, req *http.Request, next PolicyFunc) (*http.Response, error) {
	allowed, gen := cb.allowRequest()
	if !allowed {
		return nil, ErrCircuitOpen
	}
	resp, err := next(ctx, req)
	if cb.shouldTrip(resp, err) {
		cb.onFailure(gen)
	} else {
		cb.onSuccess(gen)
	}
	return resp, err
}

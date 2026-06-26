package ambatukam

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type State string

const (
	StateClosed   State = "closed"
	StateOpen     State = "open"
	StateHalfOpen State = "half-open"
)

type CircuitBreakerPolicy struct {
	cfg    CircuitConfig
	logger *slog.Logger
	hooks  Hooks
	name   string

	mu               sync.RWMutex
	state            State
	openedAt         time.Time
	halfOpenPermits  uint32
	halfOpenInFlight uint32

	failures   atomic.Uint32
	generation atomic.Uint64
}

func DefaultCircuitConfig() CircuitConfig {
	return CircuitConfig{
		FailureThreshold: 5,
		OpenDuration:     30 * time.Second,
		HalfOpenMaxReqs:  1,
	}
}

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

func (cb *CircuitBreakerPolicy) WithLogger(l *slog.Logger) *CircuitBreakerPolicy {
	if l != nil {
		cb.logger = l
	}
	return cb
}

func (cb *CircuitBreakerPolicy) WithHooks(h Hooks) *CircuitBreakerPolicy {
	cb.hooks = h
	return cb
}

func (cb *CircuitBreakerPolicy) WithName(name string) *CircuitBreakerPolicy {
	if name != "" {
		cb.name = name
	}
	return cb
}

func (cb *CircuitBreakerPolicy) State() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

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

func (cb *CircuitBreakerPolicy) allowRequest() (bool, uint64) {
	cb.mu.RLock()
	switch cb.state {
	case StateClosed:
		gen := cb.generation.Load()
		cb.mu.RUnlock()
		return true, gen
	case StateOpen:
		if time.Since(cb.openedAt) >= cb.cfg.OpenDuration {
			cb.mu.RUnlock()
			cb.mu.Lock()
			if cb.state == StateOpen && time.Since(cb.openedAt) >= cb.cfg.OpenDuration {
				cb.state = StateHalfOpen
				cb.halfOpenPermits = cb.cfg.HalfOpenMaxReqs
				cb.halfOpenInFlight = 0
				cb.generation.Add(1)
				cb.logger.Info("circuit: open -> half-open",
					slog.Uint64("permits", uint64(cb.halfOpenPermits)),
				)
				if cb.hooks.OnStateChange != nil {
					cb.hooks.OnStateChange(cb.name, StateOpen, StateHalfOpen)
				}
			}
			gen := cb.generation.Load()
			if cb.halfOpenPermits > 0 {
				cb.halfOpenPermits--
				cb.halfOpenInFlight++
				cb.mu.Unlock()
				return true, gen
			}
			cb.mu.Unlock()
			return false, gen
		}
		gen := cb.generation.Load()
		cb.mu.RUnlock()
		return false, gen
	case StateHalfOpen:
		gen := cb.generation.Load()
		if cb.halfOpenPermits > 0 {
			cb.mu.RUnlock()
			cb.mu.Lock()
			if cb.state == StateHalfOpen && cb.halfOpenPermits > 0 {
				cb.halfOpenPermits--
				cb.halfOpenInFlight++
				cb.mu.Unlock()
				return true, gen
			}
			cb.mu.Unlock()
			return false, gen
		}
		cb.mu.RUnlock()
		return false, gen
	}
	cb.mu.RUnlock()
	return false, 0
}

func (cb *CircuitBreakerPolicy) onSuccess(genAtEntry uint64) {
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
			if cb.hooks.OnStateChange != nil {
				cb.hooks.OnStateChange(cb.name, StateHalfOpen, StateClosed)
			}
		}
	}
	cb.mu.Unlock()
}

func (cb *CircuitBreakerPolicy) onFailure(genAtEntry uint64) {
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
				if cb.hooks.OnStateChange != nil {
					cb.hooks.OnStateChange(cb.name, StateClosed, StateOpen)
				}
			}
		case StateHalfOpen:
			cb.halfOpenInFlight--
			cb.state = StateOpen
			cb.openedAt = time.Now()
			cb.halfOpenPermits = 0
			cb.halfOpenInFlight = 0
			cb.generation.Add(1)
			cb.logger.Warn("circuit: half-open -> open (trial failed)")
			if cb.hooks.OnStateChange != nil {
				cb.hooks.OnStateChange(cb.name, StateHalfOpen, StateOpen)
			}
		}
	}
	cb.mu.Unlock()
}

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

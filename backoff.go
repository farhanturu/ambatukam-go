package ambatukam

import (
	"math/rand"
	"time"
)

// Backoff produces the delay to wait before the next retry attempt.
// attempt is 0-indexed: attempt=0 is the delay before the first retry
// (i.e., after the initial request failed).
type Backoff interface {
	NextDelay(attempt int) time.Duration
}

// expBackoff grows delay exponentially: initial * multiplier^attempt, capped at max.
// Jitter (if > 0) applies symmetric random jitter of ± jitter*delay.
type expBackoff struct {
	initial, max time.Duration
	multiplier   float64
	jitter       float64
}

func (e *expBackoff) NextDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	d := float64(e.initial) * pow(e.multiplier, float64(attempt))
	if d > float64(e.max) {
		d = float64(e.max)
	}
	if e.jitter > 0 {
		// symmetric jitter: ± jitter * d
		delta := d * e.jitter
		d = d - delta + rand.Float64()*2*delta
		if d < 0 {
			d = 0
		}
	}
	return time.Duration(d)
}

// ExponentialBackoff returns a Backoff that grows exponentially from initial
// up to max, with symmetric jitter of ±20% applied per delay.
func ExponentialBackoff(initial, max time.Duration, multiplier float64) Backoff {
	return &expBackoff{initial: initial, max: max, multiplier: multiplier, jitter: 0.2}
}

// constBackoff returns the same delay every attempt.
type constBackoff struct{ d time.Duration }

func (c *constBackoff) NextDelay(attempt int) time.Duration { return c.d }

// ConstantBackoff returns a Backoff that always returns d.
func ConstantBackoff(d time.Duration) Backoff { return &constBackoff{d: d} }

// linearBackoff grows by a fixed step per attempt, capped at max.
type linearBackoff struct {
	initial, max time.Duration
	step         time.Duration
}

func (l *linearBackoff) NextDelay(attempt int) time.Duration {
	d := l.initial + l.step*time.Duration(attempt)
	if d > l.max {
		return l.max
	}
	if d < 0 {
		return 0
	}
	return d
}

// LinearBackoff returns a Backoff that grows linearly: initial + step*attempt, capped at max.
func LinearBackoff(initial, max, step time.Duration) Backoff {
	return &linearBackoff{initial: initial, max: max, step: step}
}

// pow is a local integer-exponent power helper. Attempts are small non-negative
// integers in practice (0..5), so a loop avoids the overhead of math.Pow and
// any float precision surprises for chained multiplications.
func pow(base float64, exp float64) float64 {
	result := 1.0
	n := int(exp)
	if n < 0 {
		return 1.0 / pow(base, -exp)
	}
	for i := 0; i < n; i++ {
		result *= base
	}
	return result
}

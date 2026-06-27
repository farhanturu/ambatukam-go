package ambatukam

import (
	"math"
	"math/rand"
	"time"
)

type Backoff interface {
	NextDelay(attempt int) time.Duration
}

type expBackoff struct {
	initial, max time.Duration
	multiplier   float64
	jitter       float64
}

func (e *expBackoff) NextDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	d := float64(e.initial) * math.Pow(e.multiplier, float64(attempt))
	if d > float64(e.max) {
		d = float64(e.max)
	}
	if e.jitter > 0 {
		delta := d * e.jitter
		d = d - delta + rand.Float64()*2*delta
		if d < 0 {
			d = 0
		}
	}
	return time.Duration(d)
}

func ExponentialBackoff(initial, max time.Duration, multiplier, jitter float64) Backoff {
	return &expBackoff{initial: initial, max: max, multiplier: multiplier, jitter: jitter}
}

type constBackoff struct{ d time.Duration }

func (c *constBackoff) NextDelay(attempt int) time.Duration { return c.d }

func ConstantBackoff(d time.Duration) Backoff { return &constBackoff{d: d} }

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

func LinearBackoff(initial, max, step time.Duration) Backoff {
	return &linearBackoff{initial: initial, max: max, step: step}
}

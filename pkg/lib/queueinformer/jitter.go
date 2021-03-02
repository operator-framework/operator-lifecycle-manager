package queueinformer

import (
	"math"
	"math/rand"
	"time"
)

const DefaultResyncPeriod = 15 * time.Minute

type float64er interface {
	// Float64 returns a float64 in range [0.0, 1.0).
	Float64() float64
}

type realFloat64er struct{}

func (realFloat64er) Float64() float64 {
	return rand.Float64()
}

// ResyncWithJitter takes a resync interval and adds jitter within a percent difference.
// factor is a value between 0 and 1 indicating the amount of jitter
// a factor of 0.2 and a period of 10m will have a range of 8 to 12 minutes (20%)
func ResyncWithJitter(resyncPeriod time.Duration, factor float64) func() time.Duration {
	return resyncWithJitter(resyncPeriod, factor, realFloat64er{})
}

func resyncWithJitter(period time.Duration, factor float64, rand float64er) func() time.Duration {
	return func() time.Duration {
		if period < 0.0 {
			return DefaultResyncPeriod
		}
		if period > math.MaxInt64/2 { // 1281023h53m38.427387903s
			// avoid overflowing time.Duration
			return period
		}
		if factor < 0.0 || factor > 1.0 {
			return period
		}

		// The effective scale will be in [1-factor, 1+factor) because rand.Float64() is in [0.0, 1.0).
		return time.Duration((1 - factor + 2*rand.Float64()*factor) * float64(period))
	}
}

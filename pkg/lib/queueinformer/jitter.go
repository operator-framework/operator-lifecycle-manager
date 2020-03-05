package queueinformer

import (
	"math/rand"
	"time"
)

const DefaultResyncPeriod = 15 * time.Minute

// ResyncWithJitter takes a resync interval and adds jitter within a percent difference.
// factor is a value between 0 and 1 indicating the amount of jitter
// a factor of 0.2 and a period of 10m will have a range of 8 to 12 minutes (20%)
func ResyncWithJitter(resyncPeriod time.Duration, factor float64) func() time.Duration {
	return func() time.Duration {
		if factor < 0.0 || factor > 1.0 {
			return resyncPeriod
		}
		if resyncPeriod < 0.0 {
			return DefaultResyncPeriod
		}

		// if we would wrap around, return resyncPeriod
		if time.Duration((1+factor)*resyncPeriod.Minutes())*time.Minute < 0.0 {
			return resyncPeriod
		}

		min := resyncPeriod.Minutes() * (1 - factor)
		max := resyncPeriod.Minutes() * (1 + factor)

		return time.Duration(min)*time.Minute + time.Duration(rand.Float64()*(max-min))*time.Minute
	}
}

// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package xo

import (
	"github.com/irifrance/gini/z"
	"sync"
	"time"
)

// Type Ctl encapsulates low level asynchronous control
// over a Solve.  Ctl is used to implement inter.S and
// concur.U and concur.S
type Ctl struct {
	mu           sync.Mutex
	xo           *S
	cResult      chan int
	ciLearn      chan []z.Lit
	coLearn      chan []z.Lit
	cStopOrPause chan bool
	stFunc       func(stats *Stats) *Stats
}

// NewCtl creates a new controller.
func NewCtl(s *S) *Ctl {
	return &Ctl{
		xo:           s,
		cResult:      make(chan int),
		ciLearn:      make(chan []z.Lit, 128),
		coLearn:      make(chan []z.Lit, 128),
		cStopOrPause: make(chan bool),
		stFunc:       func(st *Stats) *Stats { return st }}
}

// Tick checks if the solver must stop or pause
// and if it must stop, it stops it and returns false
// otherwise, if the solver was paused, then tick
// blocks until unpause and returns true
// otherwise it just returns true.
func (c *Ctl) Tick() bool {
	select {
	case end, ok := <-c.cStopOrPause:
		if end || !ok {
			return false
		}
		// paused, other end receives to unpause
		c.cStopOrPause <- true
	default:
	}
	return true
}

// Stop stops the current call to solve and
// returns the solve result.
//
// Stop should not be called more than once, and
// if it is called, then Test should not be called
// subsequently.
func (c *Ctl) Stop() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stop()
}
func (c *Ctl) stop() int {
	select {
	case c.cStopOrPause <- true:
		res := <-c.cResult
		return res
	case res := <-c.cResult:
		return res
	}
}

// Test tests whether a result is a available
// and returns it together with whether the
// underlying Solve() has terminated.  Test returns
//
//  - 1   for SAT
//  - -1  for UNSAT
//  - 0   for UNKNOWN
//
func (c *Ctl) Test() (result int, done bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case res := <-c.cResult:
		return res, true
	default:
		return 0, false
	}
}

// Try tries to get the result within
// d time, returning 0 by default if no result is available
// within d time.
func (c *Ctl) Try(d time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	alarm := time.After(d)
	select {
	case <-alarm:
		return c.stop()
	case res := <-c.cResult:
		return res
	}
}

// Type StatsResult encapsulates a pair of stats and a solve result.
type StatsResult struct {
	Result int
	Stats  *Stats
}

// TryStats outputs stats and result for at most timeout duration
// with stat requests occuring every stFreq.
func (c *Ctl) TryStats(timeout, stFreq time.Duration) <-chan StatsResult {
	rc := make(chan StatsResult)
	st := NewStats()
	tst := NewStats()
	go func() {
		ticker := time.NewTicker(stFreq)
		defer ticker.Stop()
		alarm := time.After(timeout)
		for {
			select {
			case <-alarm:
				close(rc)
				return
			case res := <-c.cResult:
				c.stFunc(tst)
				st.Accumulate(tst)
				st2 := *st
				rc <- StatsResult{Result: res, Stats: &st2}
				close(rc)
				return
			case <-ticker.C:
				res, ok := c.Pause()
				c.stFunc(tst)
				st.Accumulate(tst)
				st2 := *st
				tst = NewStats()
				rc <- StatsResult{Result: res, Stats: &st2}
				if !ok {
					close(rc)
					return
				}
				c.Unpause()
			}
		}
	}()
	return rc
}

// Wait just waits until the Solve finishes and returns the result. Beware,
// NP-complete problem...
func (c *Ctl) Wait() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	res := <-c.cResult
	return res
}

// Pause tries to pause the underlying Solve().  If
// not sucessfule, it returns the result of Solve() together
// with false.  If successful, it returns (0, true).
//
// When successful, Pause should always be followed by Unpause
// before any other usage of c.  Pause is not re-entrant,
// Pausing a paused Solve will block indefinitely.  This is
// in line with the not-safe for multiple goroutines nature
// of the Solve API.
func (c *Ctl) Pause() (res int, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case res := <-c.cResult:
		return res, false
	case c.cStopOrPause <- false:
		c.xo.rmu.Unlock()
		return 0, true
	}
}

// Unpause resumes the solving process from a previous pause.
// Unpause will block indefinitely if the conn is not paused.
func (c *Ctl) Unpause() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.xo.rmu.Lock()
	<-c.cStopOrPause
}

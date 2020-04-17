// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package inter

import "time"

// Interface Solve represents a connection to a call to (S).Solve().
//
// Solve may be constructed by a call to (S).GoSolve().
//
// Since solves can take almost arbitrary time, we provide an asynchronous
// interface via Solve.  This interface is safe only in the sense that the
// solver will not go out of wack with multiple Solve calls in the face of
// asynchronous cancelation.
//
// This interface is NOT safe for usage in multiple goroutines
// and several caveats must be respected:
//
//  1. Once a result from the underlying Solve() is obtained, Solve should
//  no longer be used.
//  2. Every successful Pause() should be followed by Unpause before trying to
//     obtain a result.
//  3. Every unsucessful Pause should not be followed by a corresponding
//     Pause, as a result is obtained.
type Solve interface {
	// Stop stops the Solve() call and returns the result, defaulting to
	// 0 if the answer is unknown.
	Stop() int

	// Try lets Solve() run for at most d time and then returns the result.
	Try(d time.Duration) int

	// Test checks whether or not a result is ready, and if so returns it
	// together with true.  If not, it returns (0, false).
	Test() (int, bool)

	// Pause tries to pause the Solve(), returning the result of solve if any
	// and whether the Pause succeeded (ok).  If the pause did not succeed, it
	// is because the underlying Solve() returned a result.
	Pause() (res int, ok bool)

	// Unpause unpauses the Solve().  Should only be called if Pause succeeded.
	Unpause()

	// Wait blocks until there is a result.
	Wait() int
}

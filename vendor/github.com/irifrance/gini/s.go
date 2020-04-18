// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package gini

import "github.com/irifrance/gini/inter"

// NewS creates a new solver, which is the Gini
// implementation of inter.S.
func NewS() inter.S {
	return New()
}

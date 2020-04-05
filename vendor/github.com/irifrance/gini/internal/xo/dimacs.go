// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package xo

import "github.com/irifrance/gini/z"

// Type DimacsVis implements dimacs.Vis for constructing
// solvers from dimacs cnf files.
type DimacsVis struct {
	s *S
}

func (d *DimacsVis) Init(v, c int) {
	d.s = NewSVc(v, c)
}

func (d *DimacsVis) Add(m z.Lit) {
	d.s.Add(m)
}

func (d *DimacsVis) S() *S {
	return d.s
}

func (d *DimacsVis) Eof() {
}

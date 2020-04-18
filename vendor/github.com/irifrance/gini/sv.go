// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package gini

import (
	"time"

	"github.com/irifrance/gini/inter"
	"github.com/irifrance/gini/z"
)

type svWrap struct {
	S inter.S
	V *z.Vars
}

// NewSv creates an Sv implementation
func NewSv() inter.Sv {
	w := &svWrap{
		S: New(),
		V: z.NewVars()}
	return w
}

// NewSvVars creates an Sv implementation with
// the specified set of vars.
func NewSvVars(vs *z.Vars) inter.Sv {
	w := &svWrap{
		S: New(),
		V: vs}
	return w
}

func (w *svWrap) Inner() z.Lit {
	return w.V.Inner()
}

func (w *svWrap) FreeInner(m z.Lit) {
	w.V.Free(m)
}

func (w *svWrap) Assume(ms ...z.Lit) {
	w.S.Assume(w.V.ToInners(ms)...)
}

func (w *svWrap) Add(m z.Lit) {
	w.S.Add(m)
}

func (w *svWrap) MaxVar() z.Var {
	return w.S.MaxVar()
}

func (w *svWrap) Lit() z.Lit {
	return w.S.Lit()
}

func (w *svWrap) Why(dst []z.Lit) []z.Lit {
	dst = w.S.Why(dst)
	return w.V.ToOuters(dst)
}

func (w *svWrap) Value(m z.Lit) bool {
	return w.S.Value(w.V.ToInner(m))
}

func (w *svWrap) Solve() int {
	return w.S.Solve()
}

func (w *svWrap) Try(dur time.Duration) int {
	return w.S.Try(dur)
}

func (w *svWrap) GoSolve() inter.Solve {
	return w.S.GoSolve()
}

func (w *svWrap) Test(dst []z.Lit) (int, []z.Lit) {
	res := 0
	res, dst = w.S.Test(dst)
	return res, w.V.ToOuters(dst)
}

func (w *svWrap) Untest() int {
	return w.S.Untest()
}

func (w *svWrap) Reasons(dst []z.Lit, implied z.Lit) []z.Lit {
	inImplied := w.V.ToInner(implied)
	dst = w.S.Reasons(dst, inImplied)
	return w.V.ToOuters(dst)
}

func (w *svWrap) SCopy() inter.S {
	c := &svWrap{
		S: w.S.SCopy(),
		V: w.V.Copy()}
	return c
}

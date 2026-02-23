// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package z

import (
	"bytes"
	"fmt"
)

// Type Vars provides a mechanism for mapping variables in
// the presence of a stream of user supplied literals interleaved
// with a stream of application demands for a new variable (or for the
// application to free variables).
type Vars struct {
	i2o  []Var
	o2i  []Var
	free []Var
	iMax Var
	oMax Var
}

// NewVars creates a new Vars object.
func NewVars() *Vars {
	return &Vars{}
}

func (v *Vars) Copy() *Vars {
	o := &Vars{
		i2o:  make([]Var, len(v.i2o)),
		o2i:  make([]Var, len(v.o2i)),
		free: make([]Var, len(v.free)),
		iMax: v.iMax,
		oMax: v.oMax}
	copy(o.i2o, v.i2o)
	copy(o.o2i, v.o2i)
	copy(o.free, v.free)
	return o
}

// ToInner maps a user supplied literal m to an application literal.
// m may or may not have been previously referenced by the user.
func (v *Vars) ToInner(m Lit) Lit {
	u := m.Var()
	v.ensureOuterCap(u)
	w := v.o2i[u]
	if m.IsPos() {
		return w.Pos()
	}
	return w.Neg()
}

// ToInners maps a slice of user supplied literals to a slice
// of application literals.  The user supplied literals may
// or may not have been previously referenced.
//
// ToInner uses "ms" for scratch space and returns it.
func (v *Vars) ToInners(ms []Lit) []Lit {
	for i, m := range ms {
		ms[i] = v.ToInner(m)
	}
	return ms
}

// ToOuter maps an application literal to a user supplied literal,
// defaulting to LitNull if there is no corresponding user supplied
// literal.
func (v *Vars) ToOuter(m Lit) Lit {
	u := m.Var()
	v.ensureInnerCap(u)
	w := v.i2o[u]
	if w == 0 {
		return LitNull
	}
	if m.IsPos() {
		return w.Pos()
	}
	return w.Neg()
}

// ToOuters maps a slice of application literals to a slice of
// user supplied literals, omitting any application literals
// which do where not referenced by the user.
//
// ToOuters uses "ms" as scratch space and returns it.
func (v *Vars) ToOuters(ms []Lit) []Lit {
	j := 0
	for _, m := range ms {
		n := v.ToOuter(m)
		if n != LitNull {
			ms[j] = n
			j++
		}
	}
	return ms[:j]
}

// Inner produces an new application literals.  The returned literal is
// always positive.
func (v *Vars) Inner() Lit {
	fl := len(v.free)
	if fl != 0 {
		res := v.free[fl-1]
		v.free = v.free[:fl-1]
		return res.Pos()
	}
	w := Var(len(v.i2o))
	v.ensureInnerCap(w)
	return w.Pos()
}

// Free frees an application literal created by Inner.  If m is
// not an application literal, the subsequent behavior of v is
// undefined.
func (v *Vars) Free(m Lit) {
	v.free = append(v.free, m.Var())
}

// String implements stringer.
func (v *Vars) String() string {
	buf := bytes.NewBuffer(nil)
	for i, w := range v.i2o {
		if i == 0 {
			continue
		}
		buf.WriteString(fmt.Sprintf("%s %s\n", w, Var(i)))
	}
	return string(buf.Bytes())
}

func (v *Vars) ensureInnerCap(w Var) {
	for u := Var(len(v.i2o)); u <= w; u++ {
		v.i2o = append(v.i2o, 0)
	}
}

func (v *Vars) ensureOuterCap(w Var) {
	for o := Var(len(v.o2i)); o <= w; o++ {
		i := Var(len(v.i2o))
		v.o2i = append(v.o2i, i)
		v.i2o = append(v.i2o, o)
	}
}

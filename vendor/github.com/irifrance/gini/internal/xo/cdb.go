// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package xo

import (
	"bytes"
	"fmt"
	"io"

	"github.com/irifrance/gini/z"
)

// Type Cdb is the main interface to clauses.
type Cdb struct {
	Vars   *Vars
	Active *Active
	CDat   CDat

	AddLits []z.Lit
	AddVals []int8

	Bot     z.C
	Added   []z.C
	Learnts []z.C

	Tracer     Tracer
	checkModel bool

	// for multi-scheduling gc frequency
	gc *Cgc

	// stats
	stAdds         int64
	stAddUnit      int64
	stAddBin       int64
	stAddTernary   int64
	stAddBig       int64
	stAddFails     int64
	stLitAdds      int64
	stMinLits      int64
	stHeatRescales int64
}

func NewCdb(v *Vars, capHint int) *Cdb {
	if capHint < 3 {
		capHint = 3
	}
	clss := &Cdb{
		Vars:       v,
		CDat:       *NewCDat(capHint * 5),
		AddLits:    make([]z.Lit, 0, 24),
		AddVals:    make([]int8, v.Top),
		Bot:        CNull,
		Added:      make([]z.C, 0, capHint/3),
		Learnts:    make([]z.C, 0, capHint-capHint/3),
		Tracer:     nil,
		gc:         NewCgc(),
		checkModel: true}
	return clss
}

func (c *Cdb) readStats(st *Stats) {
	st.Added = c.stAdds
	c.stAdds = 0

	st.AddedUnits = c.stAddUnit
	c.stAddUnit = 0

	st.AddedBinary = c.stAddBin
	c.stAddBin = 0

	st.AddedTernary = c.stAddTernary
	c.stAddTernary = 0

	st.AddedBig = c.stAddBig
	c.stAddBig = 0

	st.AddedLits = c.stLitAdds
	c.stLitAdds = 0

	st.CHeatRescales = c.stHeatRescales
	c.stHeatRescales = 0

	st.Learnts = len(c.Learnts)

	c.gc.readStats(st)
}

func (c *Cdb) Add(m z.Lit) (z.C, z.Lit) {
	if m != z.LitNull {
		c.AddLits = append(c.AddLits, m)
		return CInf, z.LitNull
	}
	c.stLitAdds += int64(len(c.AddLits))
	c.stAdds++
	retLoc := CNull
	retLit := z.LitNull
	ms := c.AddLits
	aVals := c.AddVals
	uVals := c.Vars.Vals
	j := 0
	vars := c.Vars
	w := vars.Watches
	var n z.Lit
	for _, m := range ms {
		mv := m.Var()
		// TODO(wsc) make this work for tests without duplicating op in Solver.Add()
		if mv > vars.Max {
			vars.Max = mv
		}
		us := uVals[m]
		as := aVals[mv]
		if us == 0 && as == 0 {
			ms[j] = m
			j++
			aVals[mv] = m.Sign()
			continue
		}
		as = as * m.Sign()
		if us == 1 || as == -1 {
			retLoc = CInf
			c.stAddFails++
			c.stMinLits += int64(len(ms))
			goto Done
		}
		if us == -1 || as == 1 {
			continue
		}
		panic("unreachable")
	}
	c.stMinLits += int64(len(ms) - j)
	retLoc = c.CDat.AddLits(MakeChd(false, 0, j), ms[0:j])
	c.Added = append(c.Added, retLoc)
	if j == 0 {
		c.Bot = retLoc
		goto Done
	}
	if j == 1 {
		// nb uVals set outside this in solver.Add
		retLit = ms[0]
		c.stAddUnit++
		goto Done
	}
	if j == 2 {
		c.stAddBin++
	}
	if j == 3 {
		c.stAddTernary++
	}
	if j > 3 {
		c.stAddBig++
	}
	// add watch
	w = c.Vars.Watches
	m = ms[0]
	n = ms[1]
	w[m] = append(w[m], MakeWatch(retLoc, n, j == 2))
	w[n] = append(w[n], MakeWatch(retLoc, m, j == 2))

Done:
	for _, m := range c.AddLits {
		aVals[m.Var()] = 0
	}
	c.AddLits = c.AddLits[:0]
	return retLoc, retLit
}

func (c *Cdb) Remove(cs ...z.C) {
	c.gc.Remove(c, cs...)
}

func (c *Cdb) Learn(ms []z.Lit, lbd int) z.C {
	ret := c.CDat.AddLits(MakeChd(true, lbd, len(ms)), ms)
	if c.Active != nil {
		is := c.Active.IsActive
		occs := c.Active.Occs
		for _, m := range ms {
			mv := m.Var()
			if !is[mv] {
				continue
			}
			if m.IsPos() {
				panic("positive act lit")
			}
			occs[mv] = append(occs[mv], ret)
		}
	}
	msLen := len(ms)
	switch msLen {
	case 0:
		c.Bot = ret
	case 1:
		// nothing to do here
	default:
		w := c.Vars.Watches
		m := ms[0]
		n := ms[1]
		w[m] = append(w[m], MakeWatch(ret, n, msLen == 2))
		w[n] = append(w[n], MakeWatch(ret, m, msLen == 2))
	}
	c.Learnts = append(c.Learnts, ret)
	c.gc.Tick()
	return ret
}

func (c *Cdb) InUse(o z.C) bool {
	m := c.CDat.D[o]
	return m != z.LitNull && c.Vars.Reasons[m.Var()] == o
}

func (c *Cdb) IsBinary(p z.C) bool {
	d := c.CDat
	return d.Len > int(p+2) && d.D[p+2] == z.LitNull
}

func (c *Cdb) IsUnit(p z.C) bool {
	return c.CDat.D[p+1] == z.LitNull
}

func (c *Cdb) Chd(p z.C) Chd {
	return c.CDat.Chd(p)
}

func (c *Cdb) Bump(p z.C) {
	if c.CDat.Bump(p) {
		D := c.CDat.D
		for _, p := range c.Added {
			D[p-1] = z.Lit(Chd(D[p-1]).Decay())
		}
		for _, p := range c.Learnts {
			D[p-1] = z.Lit(Chd(D[p-1]).Decay())
		}
		c.stHeatRescales++
		c.CDat.bumpInc /= 2
	}
}

func (c *Cdb) Decay() {
	c.CDat.Decay()
}

func (c *Cdb) MaybeCompact() (int, int, int) {
	gc := c.gc
	if !gc.Ready() {
		return 0, 0, 0
	}
	return gc.Compact(c)
}

func (c *Cdb) Size(p z.C) int {
	return int(c.CDat.Next(p) - p)
}

// unlinks all the clauses in rms from watchlists
// nb supposes c.Learnts does not have any locs in "rms"
// or will not before return to normal solving, outside of
// clause garbage collection.
func (c *Cdb) Unlink(rms []z.C) {
	d := c.CDat.D
	wLits := c.AddLits
	wVals := c.AddVals
	rMap := make(map[z.C]bool, len(rms))
	for _, p := range rms {
		rMap[p] = true
		for _, m := range [...]z.Lit{d[p], d[p+1]} {
			mv := m.Var()
			if wVals[mv] == 2 || wVals[mv] == m.Sign() {
				continue
			}
			if wVals[mv] == 0 {
				wVals[mv] = m.Sign()
			} else {
				wVals[mv] = 2
			}
			wLits = append(wLits, m)
		}
	}

	for _, m := range wLits {
		ws := c.Vars.Watches[m]

		j := 0
		for _, w := range ws {
			p := w.C()
			_, ok := rMap[p]
			if ok {
				continue
			}
			ws[j] = w
			j++
		}
		c.Vars.Watches[m] = ws[:j]
	}

	for _, m := range wLits {
		wVals[m.Var()] = 0
	}
	c.AddLits = c.AddLits[:0]
}

func (c *Cdb) SetTracer(t Tracer) {
	c.Tracer = t
}

func (c *Cdb) Write(w io.Writer) error {
	hdr := []byte(fmt.Sprintf("p cnf %d %d\n", c.Vars.Max, len(c.Added)))
	n := 0
	for n < len(hdr) {
		m, e := w.Write(hdr[n:])
		n += m
		if e != nil {
			return e
		}
	}
	return c.CDat.Dimacs(w)
}

func (c *Cdb) String() string {
	buf := bytes.NewBuffer(nil)
	c.Write(buf)
	return string(buf.Bytes())
}

func (c *Cdb) Lits(p z.C, ms []z.Lit) []z.Lit {
	d := c.CDat.D
	var m z.Lit
	for {
		m = d[p]
		if m == z.LitNull {
			break
		}
		p++
		ms = append(ms, m)
	}
	return ms
}

func (c *Cdb) ForallAdded(f func(p z.C, h Chd, ms []z.Lit)) {
	c.ForallSlice(f, c.Added)
}

func (c *Cdb) ForallLearnts(f func(p z.C, h Chd, ms []z.Lit)) {
	c.ForallSlice(f, c.Learnts)
}

func (c *Cdb) Forall(f func(p z.C, h Chd, ms []z.Lit)) {
	c.ForallAdded(f)
	c.ForallLearnts(f)
}

func (c *Cdb) ForallSlice(f func(p z.C, h Chd, ms []z.Lit), ps []z.C) {
	ms := make([]z.Lit, 0, 32)
	dat := c.CDat
	for _, p := range ps {
		h := dat.Chd(p)
		ms = ms[:0]
		ms = dat.Load(p, ms)
		f(p, h, ms)
	}
}

// Debug self check code, not used in any production entrypoint.
func (c *Cdb) CheckWatches() []error {
	if len(c.gc.rmq) > 0 {
		panic("cannot check watches when pending unlinked clauses are not compacted.\nCall Cgc.CompactCDat.")
	}
	watches := c.Vars.Watches
	dat := c.CDat.D
	signs := c.Vars.Vals
	errs := make([]error, 0)

	for i, ws := range watches {
		if i < 2 {
			continue
		}
		m := z.Lit(i)
		for _, w := range ws {
			p := w.C()
			if dat[p] != m && dat[p+1] != m {
				errs = append(errs, fmt.Errorf("%s, %s: not in pos[0,1]", m, p))
			}
		}
	}
	c.Forall(func(p z.C, h Chd, ms []z.Lit) {
		if len(ms) < 2 {
			return
		}
		for i, m := range ms[:2] {
			found := false
			for _, w := range watches[m] {
				q := w.C()
				if q == p {
					found = true
					break
				}
			}
			if !found {
				errs = append(errs, fmt.Errorf("lit %s in pos %d of %s but not in watches", m, i, p))
			}
		}
		if signs[ms[0]] == 1 || signs[ms[1]] == 1 {
			return
		}
		if signs[ms[0]] == -1 || signs[ms[1]] == -1 {
			errs = append(errs, fmt.Errorf("not solved and false pos[0,1]: %s %s %s", ms[0], ms[1], p))
		}
	})
	return errs
}

// by default, called when sat as a sanity check.
func (c *Cdb) CheckModel() []error {
	if !c.checkModel {
		return nil
	}
	if c.Active != nil {
		// deactivations and simplificaations remove Added clauses, which are unlinked
		// until sufficiently large to compact.  compaction
		// then cleans up Added, which we need here.
		c.gc.CompactCDat(c)
	}
	var m z.Lit
	D := c.CDat.D
	signs := c.Vars.Vals
	errs := make([]error, 0)
	for _, p := range c.Added {
		q := p
		for {
			m = D[p]
			if m == z.LitNull {
				errs = append(errs, fmt.Errorf("didn't satisfy %s %s", q, c.Lits(q, nil)))
				break
			}
			if signs[m] == 1 {
				//fmt.Printf("satisfied %s with %s\n", c.Lits(q, nil), m)
				break
			}
			p++
		}
	}
	return errs
}

// NB also Active is copied in S.Copy and placed in resulting
// copied cdb, so we don't copy Active here.
func (c *Cdb) CopyWith(ov *Vars) *Cdb {
	other := &Cdb{
		Vars:    ov,
		AddLits: make([]z.Lit, len(c.AddLits), cap(c.AddLits)),
		AddVals: make([]int8, len(c.AddVals), cap(c.AddVals)),
		Bot:     c.Bot,
		Added:   make([]z.C, len(c.Added), cap(c.Added)),
		Learnts: make([]z.C, len(c.Learnts), cap(c.Learnts))}
	copy(other.AddLits, c.AddLits)
	copy(other.AddVals, c.AddVals)
	copy(other.Added, c.Added)
	copy(other.Learnts, c.Learnts)
	c.CDat.CopyTo(&other.CDat)
	other.gc = c.gc.Copy()
	other.checkModel = c.checkModel
	return other
}

func (c *Cdb) growToVar(u z.Var) {
	u++
	av := make([]int8, u)
	copy(av, c.AddVals)
	c.AddVals = av

}

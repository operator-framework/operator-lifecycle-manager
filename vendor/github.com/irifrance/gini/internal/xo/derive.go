// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package xo

import (
	"fmt"

	"github.com/irifrance/gini/z"
)

type Deriver struct {
	Cdb   *Cdb
	Vars  *Vars
	Guess *Guess
	Trail *Trail

	CLits []z.Lit
	SLits []z.Lit
	RLits []z.Lit
	Lvls  []bool
	Rdnt  []int8
	Seen  []bool

	Conflicts  int64
	Learnt     int64
	LearntLits int64
	RedLits    int64
}

func NewDeriver(cdb *Cdb, g *Guess, t *Trail) *Deriver {
	return &Deriver{
		Cdb:        cdb,
		Vars:       cdb.Vars,
		Guess:      g,
		Trail:      t,
		CLits:      make([]z.Lit, 0, 1024),
		SLits:      make([]z.Lit, 0, 1024),
		RLits:      make([]z.Lit, 0, 1024),
		Lvls:       make([]bool, len(cdb.Vars.Levels)),
		Rdnt:       make([]int8, len(cdb.Vars.Levels)),
		Seen:       make([]bool, cdb.Vars.Top),
		Conflicts:  0,
		Learnt:     0,
		LearntLits: 0,
		RedLits:    0}
}

func (d *Deriver) CopyWith(cdb *Cdb, g *Guess, t *Trail) *Deriver {
	other := &Deriver{
		Cdb:   cdb,
		Vars:  cdb.Vars,
		Guess: g,
		Trail: t,
		CLits: make([]z.Lit, len(d.CLits), cap(d.CLits)),
		RLits: make([]z.Lit, len(d.RLits), cap(d.RLits)),
		Lvls:  make([]bool, len(d.Lvls), cap(d.Lvls)),
		Rdnt:  make([]int8, len(d.Rdnt), cap(d.Rdnt)),
		Seen:  make([]bool, len(d.Seen), cap(d.Seen))}
	copy(other.CLits, d.CLits)
	copy(other.RLits, d.RLits)
	copy(other.Lvls, d.Lvls)
	copy(other.Rdnt, d.Rdnt)
	copy(other.Seen, d.Seen)
	return other
}

type Derived struct {
	P           z.C
	Unit        z.Lit
	Size        int
	TargetLevel int
}

func (d *Deriver) String() string {
	return fmt.Sprintf("%d/%d/%d (%.2f%%)", d.Learnt, d.LearntLits, d.RedLits,
		100.0*float64(d.RedLits)/float64(d.RedLits+d.LearntLits))
}

func (d *Deriver) Derive(x z.C) *Derived {
	d.Conflicts++
	// find 1uip
	count := 0
	p := x
	cdb := d.Cdb
	cdb.Bump(x)
	ldb := cdb.CDat.D
	trail := d.Trail.D
	guess := d.Guess

	aLevels := d.Vars.Levels
	vLevel := 0
	curLevel := d.Trail.Level
	LvlP := d.Lvls
	d.CLits = d.CLits[:0]
	cLits := append(d.CLits, z.LitNull) // 1uip, will be replaced later
	sLits := d.SLits[:0]

	result := &Derived{}
	Seen := d.Seen
	reasons := d.Vars.Reasons
	var m z.Lit
	var v z.Var
	lbd := 0

	for i := d.Trail.Tail - 1; i >= 0; i-- {
		if p != CNull {
			// count/mark lits in reason clause or conflict
			// p is normal z.C for conflict, +1 for unit.
			for {
				m = ldb[p]
				if m == z.LitNull {
					break
				}
				p++
				v = m.Var()
				if Seen[v] {
					continue
				}
				Seen[v] = true

				vLevel = aLevels[v]
				// TBD: proof
				if vLevel == 0 {
					continue
				}
				if vLevel != curLevel {
					cLits = append(cLits, m)
					if result.TargetLevel < vLevel {
						result.TargetLevel = vLevel
					}
					if !LvlP[vLevel] {
						LvlP[vLevel] = true
						lbd++
					}

					continue
				}
				sLits = append(sLits, m)
				guess.Bump(m)
				count++
			}
			p = CNull
		}
		m = trail[i]
		v = m.Var()
		if !Seen[v] {
			continue
		}
		count--
		if count == 0 { // 1uip
			cLits[0] = m.Not()
			guess.Bump(m)
			break
		}
		p = reasons[v]
		cdb.Bump(p)
		p++
	}
	// cleanup seen
	for _, m := range cLits {
		v = m.Var()
		if Seen[v] {
			Seen[v] = false
		}
	}
	for _, m := range sLits {
		v = m.Var()
		Seen[v] = false
	}
	d.SLits = sLits[:0]
	d.CLits = cLits

	// minimize
	d.minimize()

	// add/construct result
	result.P = cdb.Learn(d.CLits, lbd)
	result.Unit = cLits[0]
	result.Size = len(cLits)

	// and record some exciting stats
	d.Learnt++

	return result
}

func (d *Deriver) minimize() int {
	cLits := d.CLits
	rdnt := d.Rdnt
	guess := d.Guess
	// set rdnt cache for each lit in learned clause
	for i := 1; i < len(cLits); i++ {
		rdnt[cLits[i].Var()] = 1
	}
	// dfs over each non-1uip lit, ignoring
	// rdnt cache (on entry point only) to dfs
	j := 1
	i := 1
	for ; i < len(cLits); i++ {
		m := cLits[i]
		if d.isRdnt(m) {
			continue
		}
		guess.Bump(m)
		cLits[j] = m
		j++
	}
	res := i - j

	// some stats
	d.LearntLits += int64(j)
	d.RedLits += int64(res)

	// set up cLits len
	cLits = cLits[:j]
	d.CLits = cLits

	// clean up minimize
	// d.RLits contains all lits visited in isRdnt()
	for _, m := range d.RLits {
		rdnt[m.Var()] = 0
	}
	d.RLits = d.RLits[:0]

	// clean up levels (nb ok to used minimized lits, there is a rep
	// for each level present)
	lvls := d.Lvls
	aLevels := d.Vars.Levels
	for _, m := range d.CLits {
		v := m.Var()
		lvl := aLevels[v]
		if lvls[lvl] {
			lvls[lvl] = false
		}
	}
	return res
}

func (d *Deriver) isRdnt(m z.Lit) bool {
	d.Rdnt[m.Var()] = 0
	res := d.isRdntRec(m)
	d.Rdnt[m.Var()] = 1
	return res
}

func (d *Deriver) isRdntRec(m z.Lit) bool {
	v := m.Var()
	switch d.Rdnt[v] {
	case 0:
		d.RLits = append(d.RLits, m)
		lvl := d.Vars.Levels[v]
		if !d.Lvls[lvl] {
			d.Rdnt[v] = -1
			return false
		}
		p := d.Vars.Reasons[v]
		if p == CNull {
			d.Rdnt[v] = -1
			return false
		}
		db := d.Cdb.CDat.D
		for p = p + 1; ; p++ {
			n := db[p]
			if n == z.LitNull {
				break
			}
			if !d.isRdntRec(n) {
				d.Rdnt[v] = -1
				return false
			}
		}
		d.Rdnt[v] = 1
		return true
	case 1:
		return true
	case -1:
		return false
	default:
		panic("unexpected Rdnt")
	}
}

func (d *Deriver) readStats(st *Stats) {
	st.Conflicts += d.Conflicts
	d.Conflicts = 0
	st.LearntLits += d.LearntLits
	d.LearntLits = 0
	st.MinLits += d.RedLits
	d.RedLits = 0
}

func (d *Deriver) growToVar(u z.Var) {
	N := int(u)
	lvls := make([]bool, N)
	copy(lvls, d.Lvls)
	d.Lvls = lvls

	seen := make([]bool, N)
	copy(seen, d.Seen)
	d.Seen = seen

	rdnt := make([]int8, N)
	copy(rdnt, d.Rdnt)
	d.Rdnt = rdnt
}

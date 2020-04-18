// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package xo

import (
	"sort"

	"github.com/irifrance/gini/z"
)

// Type Cgc encapsulates clause compaction/garbage collection.
//
// This is separate from, and sometimes calls CDat compaction
// which is another issue.
type Cgc struct {
	luby      *Luby
	factor    uint
	stopWatch uint // make int and regularize diff between tick and compact?

	rmq []z.C

	rmLits int
	rmd    int

	// stats
	stCompacts int64
	stCDatGcs  int64
	stRmd      int64
	stRmdLits  int64
}

// NewCgc creates a new Cgc object
func NewCgc() *Cgc {
	l := NewLuby()
	return &Cgc{
		luby:       l,
		factor:     2048,
		stopWatch:  2048 * l.Next(),
		rmq:        make([]z.C, 0, 1024),
		rmLits:     0,
		rmd:        0,
		stCompacts: 0,
		stCDatGcs:  0,
		stRmd:      0,
		stRmdLits:  0}
}

// Copy makes a copy of c.
func (c *Cgc) Copy() *Cgc {
	l := NewLuby()
	*l = *c.luby
	other := &Cgc{
		luby:      l,
		factor:    2048,
		stopWatch: 2048 * l.Next(),
		rmq:       make([]z.C, len(c.rmq), cap(c.rmq)),
		rmLits:    c.rmLits,
		rmd:       c.rmd}
	copy(other.rmq, c.rmq)
	return other
}

// Tick is called every time there is a learned clause.
// it keeps track of virtual time for the gc.
func (c *Cgc) Tick() {
	if c.stopWatch > 0 {
		c.stopWatch--
	}
}

// Ready tests whether a clause gc is ready to occur.
func (c *Cgc) Ready() bool {
	return c.stopWatch <= 0
}

func (gc *Cgc) Remove(cdb *Cdb, cs ...z.C) {
	rmLitCount := 0
	for _, c := range cs {
		rmLitCount += cdb.Size(c)
	}

	gc.rmq = append(gc.rmq, cs...)
	gc.rmd += len(cs)
	gc.stRmd += int64(len(cs))
	gc.stRmdLits += int64(rmLitCount)
	gc.rmLits += rmLitCount
	if !cdb.CDat.CompactReady(gc.rmd, gc.rmLits) {
		cdb.Unlink(cs)
		return
	}
	//  free literal data
	gc.CompactCDat(cdb) // also unlinks
}

func uniq(cs []z.C) []z.C {
	if len(cs) <= 1 {
		return cs
	}
	i := 0
	j := 1
	last := cs[0]
	var cur z.C
	N := len(cs)
	for j < N {
		cur = cs[j]
		if cur != last {
			i++
			cs[i] = cur
			last = cur
		}
		j++
	}
	return cs[:i+1]
}

// Compact runs a clause gc, which in turn sometimes
// compacts the underlying CDat.  Compact returns
//
//  (num clauses removed, num clauses cdat removed, num freed uint32s)
func (c *Cgc) Compact(cdb *Cdb) (int, int, int) {
	c.stCompacts++
	c.stopWatch = c.luby.Next() * c.factor
	learnts := cdb.Learnts
	cDat := cdb.CDat
	g := gcLearnts{
		learnts: learnts,
		cdat:    &cDat}
	g.Sort()

	sz := len(cdb.Learnts)
	top := sz - 1
	lim := sz / 2
	rmLitCount := 0
	rms := make([]z.C, 0, lim)
	// maybe should go in ascending order...--nope!
	for n := top; n >= 0; n-- {
		p := learnts[n]
		if cdb.InUse(p) {
			continue
		}
		if cdb.IsBinary(p) {
			continue
		}
		if cdb.IsUnit(p) {
			continue
		}
		// ok, add it to rm for removal below.
		rms = append(rms, p)
		learnts[n], learnts[top] = learnts[top], p
		learnts = learnts[:top]
		top--
		rmLitCount += cdb.Size(p)
		lim--
		if lim <= 0 {
			break
		}
	}
	cdb.Learnts = learnts
	c.rmq = append(c.rmq, rms...)
	c.rmd += len(rms)
	c.stRmd += int64(len(rms))
	c.stRmdLits += int64(rmLitCount)
	c.rmLits += rmLitCount
	if !cDat.CompactReady(c.rmd, c.rmLits) {
		cdb.Unlink(rms)
		return len(rms), 0, 0
	}

	//  free literal data
	nc, n := c.CompactCDat(cdb)
	return len(rms), nc, n
}

// NB this is only from cgc.Compact
func (c *Cgc) CompactCDat(cdb *Cdb) (int, int) {
	c.stCDatGcs++
	c.rmLits = 0
	c.rmd = 0
	crm := c.rmq
	cLocSlice(crm).Sort()
	if cdb.Active != nil {
		crm = uniq(crm)
		// otherwise, it's only learnts and uniq by construction.
	}
	relocMap, freed := cdb.CDat.Compact(crm)
	c.relocate(cdb, relocMap)
	c.rmq = c.rmq[:0]
	return len(crm), freed
}

func (c *Cgc) relocate(cdb *Cdb, rlm map[z.C]z.C) {
	cdb.Learnts = relocateSlice(cdb.Learnts, rlm)
	cdb.Added = relocateSlice(cdb.Added, rlm)
	// reasons
	cdb.Vars.Reasons = relocateSlice(cdb.Vars.Reasons, rlm)
	// watches
	c.relocateWatches(cdb, rlm)
	// activation occs
	if cdb.Active != nil {
		cdb.Active.CRemap(rlm)
	}
}

func (c *Cgc) readStats(st *Stats) {
	st.Compactions += c.stCompacts
	c.stCompacts = 0

	st.CDatGcs += c.stCDatGcs
	c.stCDatGcs = 0

	st.Removed += c.stRmd
	c.stRmd = 0

	st.RemovedLits += c.stRmdLits
	c.stRmdLits = 0
}

func (c *Cgc) relocateWatches(cdb *Cdb, rlm map[z.C]z.C) {
	watches := cdb.Vars.Watches
	for i, ws := range watches {
		if i < 2 {
			continue
		}
		m := z.Lit(i)
		j := 0
		for _, w := range ws {
			p := w.C()
			q, ok := rlm[p]
			if !ok { // not relocated or removed.
				ws[j] = w
				j++
				continue
			}
			// NB this condition only makes sense when "ok" is true,
			// then we know p was removed.
			if q == CNull {
				continue
			}
			// relocated and kept
			ws[j] = w.Relocate(q)
			j++
		}
		watches[m] = ws[:j]
	}
}

type gcLearnts struct {
	learnts []z.C
	cdat    *CDat
}

func (c *gcLearnts) Len() int {
	return len(c.learnts)
}

func (c *gcLearnts) Swap(i, j int) {
	ps := c.learnts
	ps[i], ps[j] = ps[j], ps[i]
}

func (c *gcLearnts) Less(i, j int) bool {
	ps := c.learnts
	p, q := ps[i], ps[j]
	d := c.cdat
	ph, qh := d.Chd(p), d.Chd(q)

	pd, qd := ph.Lbd(), qh.Lbd()
	if pd < 8 || qd < 8 {
		if pd < qd {
			return true
		}
		if pd > qd {
			return false
		}
	}
	pt, qt := ph.Heat(), qh.Heat()
	if pt > qt {
		return true
	}
	if pt < qt {
		return false
	}

	psz, qsz := ph.Size(), qh.Size()
	if psz < qsz {
		return true
	}
	if psz > qsz {
		return false
	}
	return q < p
}

func (p *gcLearnts) Sort() {
	sort.Sort(p)
}

type cLocSlice []z.C

func (p cLocSlice) Len() int {
	return len(p)
}

func (p cLocSlice) Swap(i, j int) {
	p[i], p[j] = p[j], p[i]
}

func (p cLocSlice) Less(i, j int) bool {
	return p[i] < p[j]
}

func (p cLocSlice) Sort() {
	sort.Sort(p)
}

func relocateSlice(ps []z.C, m map[z.C]z.C) []z.C {
	j := 0
	for _, p := range ps {
		q, ok := m[p]
		if !ok {
			ps[j] = p
			j++
			continue
		}
		if q == CNull {
			continue
		}
		ps[j] = q
		j++
	}
	return ps[:j]
}

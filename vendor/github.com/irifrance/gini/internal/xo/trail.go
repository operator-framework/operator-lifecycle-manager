// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package xo

import (
	"bytes"
	"fmt"

	"github.com/irifrance/gini/z"
)

type late struct {
	m z.Lit
	r z.C
}

type Trail struct {
	Cdb   *Cdb
	Vars  *Vars
	Guess *Guess
	Head  int
	Tail  int
	Level int
	D     []z.Lit
	lates []late

	Props   int64
	MaxTail int
}

func NewTrail(cdb *Cdb, guess *Guess) *Trail {
	return &Trail{
		Cdb:   cdb,
		Vars:  cdb.Vars,
		Guess: guess,
		Head:  0,
		Tail:  0,
		Level: 0,
		D:     make([]z.Lit, cdb.Vars.Top),
		lates: make([]late, 0, 128)}
}

func (t *Trail) CopyWith(cdb *Cdb, guess *Guess) *Trail {
	other := &Trail{
		Cdb:   cdb,
		Vars:  cdb.Vars,
		Guess: guess,
		Head:  t.Head,
		Tail:  t.Tail,
		Level: t.Level,
		D:     make([]z.Lit, len(t.D), cap(t.D)),
		lates: make([]late, len(t.lates), cap(t.lates))}
	copy(other.D, t.D)
	copy(other.lates, t.lates)
	return other
}

func (t *Trail) Assign(m z.Lit, c z.C) {
	v := m.Var()
	t.D[t.Tail] = m
	t.Tail++
	vars := t.Vars
	vars.Reasons[v] = c

	if c == CNull {
		t.Level++
	}
	vars.Levels[v] = t.Level
	vars.Vals[m] = 1
	vars.Vals[m.Not()] = -1
	//log.Printf("assigned %s %s\n", m, c)
}

func (t *Trail) Back(trgLevel int) {
	if t.Level <= trgLevel {
		return
	}

	lvls := t.Vars.Levels
	vals := t.Vars.Vals
	reasons := t.Vars.Reasons
	guess := t.Guess

	i := t.Tail
	dat := t.D

	var m z.Lit
	var v z.Var
	var l int

	for i > 0 {
		i--
		m = dat[i]
		v = m.Var()
		l = lvls[v]
		if l == trgLevel {
			t.Head = i + 1
			t.Tail = t.Head
			t.Level = trgLevel
			return
		}
		vals[m] = 0
		vals[m.Not()] = 0
		reasons[v] = CNull
		lvls[v] = -1
		guess.Push(m) // actually only adds it if it is not already there.
	}
	// this happens when there are no units at level 0 and backtrack to level 0.
	t.Head = 0
	t.Tail = 0
	t.Level = 0
}

func (t *Trail) Prop() z.C {
	vals := t.Vars.Vals
	data := t.D
	watches := t.Vars.Watches
	cdb := t.Cdb
	cdat := cdb.CDat.D
	var mWatches []Watch
	var p, q z.C
	var o z.Lit
	var m z.Lit
	var n z.Lit
	var oSign, nSign int8
	orgHead := t.Head

	for t.Head < t.Tail {
		m = data[t.Head].Not()
		t.Head++
		mWatches = watches[m]
		j := 0
		for i, w := range mWatches {
			o = w.Other()
			oSign = vals[o]
			if oSign == 1 {
				if j != i {
					mWatches[j] = w
				}
				j++
				continue
			}

			q = w.C()
			if w.IsBinary() {
				if cdat[q] == m {
					cdat[q], cdat[q+1] = o, m
				}
				if oSign == 0 {
					// unit
					t.Assign(o, q)
					if j != i {
						mWatches[j] = w
					}
					j++
					continue
				}
				// else oSign = -1, conflict
				e := len(mWatches) - (i - j)
				copy(mWatches[j:e], mWatches[i:])
				watches[m] = mWatches[:e]
				t.Head = t.Tail
				t.Props += int64(t.Head - orgHead)
				return q
			}

			// not binary, touch clause mem read-only in one place and continue
			// if possible
			n = cdat[q]
			if n == m {
				n = cdat[q+1]
			}
			if n != o {
				oSign = vals[n]
				if oSign == 1 {
					if j != i {
						mWatches[j] = w
					}
					j++
					continue
				}
			}

			// scan rest of clause for non-false lit
			for p = q + 2; ; p++ {
				n = cdat[p]
				if n == z.LitNull { // end of clause
					//fmt.Printf("end of clause\n")
					if oSign == 0 {
						//fmt.Printf("\tunit...\n")
						// unit
						if cdat[q] == m {
							cdat[q], cdat[q+1] = cdat[q+1], m
						}
						t.Assign(cdat[q], q)
						if j != i {
							mWatches[j] = w
						}
						j++
						break
					}
					// oSign == -1
					// conflict
					e := len(mWatches) - (i - j)
					copy(mWatches[j:e], mWatches[i:])
					watches[m] = mWatches[:e]
					t.Head = t.Tail
					t.Props += int64(t.Head - orgHead)
					return q
				}

				nSign = vals[n]
				if nSign == -1 {
					continue
				}
				// we have a new watch, swap things.
				if cdat[q] == m {
					cdat[q], cdat[p] = n, m
				} else {
					cdat[q+1], cdat[p] = n, m
				}
				// add new watch
				watches[n] = append(watches[n], MakeWatch(q, m, false))
				// implicitly remove old watch by not setting mWatches[j]
				break
			}
		}
		watches[m] = mWatches[:j]
		//fmt.Printf("after prop %s, watches %s\n", m, watches[m])
	}
	// stats
	t.Props += int64(t.Head - orgHead)
	if t.Tail > t.MaxTail {
		t.MaxTail = t.Tail
	}
	return CNull
}

// backWithLates is used by Untest to go back one level of assumptions.
// since Solve() can put learned clauses under assumptions
// "late" in the trail, backWithLates is used to find these clauses
// and go back to previous assumption level with everything in order.
func (t *Trail) backWithLates(prevLevel int) {
	if prevLevel >= t.Level {
		return
	}
	reasons := t.Vars.Reasons
	levels := t.Vars.Levels
	lates := t.lates[:0]
	cD := t.Cdb.CDat.D
	lvl := t.Level
	for i := t.Tail - 1; i >= 0; i-- {
		m := t.D[i]
		r := reasons[m.Var()]
		if r == CNull {
			lvl--
			if lvl == prevLevel {
				break
			}
			continue
		}
		q := r + 1
		hasCur := false
		for !hasCur {
			n := cD[q]
			if n == z.LitNull {
				break
			}
			nLvl := levels[n.Var()]
			if nLvl > prevLevel {
				hasCur = true
			}
			q++
		}
		if !hasCur {
			lates = append(lates, late{m: m, r: r})
		}
	}
	t.Back(prevLevel)
	for _, late := range lates {
		// TBD: correct levels (also in main solve loop)
		t.Assign(late.m, late.r)
	}
	t.Tail = t.Head
}

func (t *Trail) String() string {
	var buf bytes.Buffer
	buf.WriteString("Trail:\n\tLevel 0:\n")
	level := 0
	reasons := t.Vars.Reasons
	for i := 0; i < t.Tail; i++ {
		m := t.D[i]
		r := reasons[m.Var()]
		if r == CNull {
			level++
			buf.WriteString(fmt.Sprintf("\tLevel %d:\n", level))
		}
		buf.WriteString(fmt.Sprintf("\t\t%s %s\n", m, r))
	}
	buf.WriteString("\n")
	return buf.String()
}

func (t *Trail) readStats(st *Stats) {
	st.Props += t.Props
	if st.MaxTrail < t.MaxTail {
		st.MaxTrail = t.MaxTail
	}
	t.Props = 0
}

func (t *Trail) growToVar(u z.Var) {
	d := make([]z.Lit, u*2)
	copy(d, t.D)
	t.D = d
}

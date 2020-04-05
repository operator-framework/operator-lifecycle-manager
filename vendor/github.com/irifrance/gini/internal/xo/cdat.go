// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package xo

import (
	"bytes"
	"fmt"
	"io"
	"math"

	"github.com/irifrance/gini/z"
)

// Type CDat: basic operations for storing all the literals (and Chds) in a CNF
type CDat struct {
	D       []z.Lit // contains Chds as well, same underlying type
	Len     int
	Cap     int
	ClsLen  int
	bumpInc uint32
}

// The Layout is as follows
//
//  Each clause is indexed by a z.C, which is a uint32 index into CDat.D.  The location is
//  the beginning of the list of literals in the clause, which is terminated by z.LitNull.
//  Each location is preceded by a Chd giving clause metadata.  Since the underlying type of
//  Chd and z.Lit are uint32, this is fine.
//
//  Rather than allocate lots of bits in the Chd to the size, we only allocate 5 bits (max size
//  of 31).  For each actual size s, we store
//
//    s & 31
//
//  To find the end of a clause at a given location, we iterate through the possible sizes
//  with the same modulus, which is usually 1 operation.
//
func NewCDat(cap int) *CDat {
	if cap < 3 {
		cap = 3
	}
	res := &CDat{
		D:       make([]z.Lit, cap, cap),
		Len:     0,
		Cap:     cap,
		ClsLen:  0,
		bumpInc: 1}
	return res
}

// func AddLits adds literals to the underlying data.  The clause header
// hdr must be consistent.
func (c *CDat) AddLits(hdr Chd, ms []z.Lit) z.C {
	ms = append(ms, z.LitNull)
	mLen := len(ms)
	cLen := c.Len
	eLen := cLen + mLen + 1
	if eLen >= c.Cap {
		c.grow(eLen)
	}
	d := c.D
	d[cLen] = z.Lit(hdr)
	cLen++
	res := z.C(cLen)
	copy(c.D[cLen:eLen], ms)
	c.Len = eLen
	c.ClsLen++
	return res
}

// func Chd retrieves the Chd associated with a z.C
func (c *CDat) Chd(loc z.C) Chd {
	return Chd(c.D[loc-1])
}

// func SetChd sets the Chd associated with a z.C
// if the size is wrong, everything will break.
func (c *CDat) SetChd(loc z.C, hd Chd) {
	c.D[loc-1] = z.Lit(hd)
}

// compute the next location
func (c *CDat) Next(loc z.C) z.C {
	D := c.D
	hd := Chd(D[loc-1])
	szModulus := hd.Size()
	i := uint32(0)
	j := uint32(loc) + szModulus
	dLen := uint32(len(D))
	for j < dLen {
		if D[j] == z.LitNull {
			return z.C(j + 2)
		}
		i++
		j = uint32(loc) + ((i << szBits) | szModulus)
	}
	panic("unreachable")
}

// ComactReady returns whether a compaction of the data is ready.
func (c *CDat) CompactReady(nc, nl int) bool {
	free := nc*2 + nl
	return c.Len/2 < free
}

// Compact the storage by removing clauses with locations in rm
// pre: rm is sorted in ascending z.C order
//
// this just compacts the data and returns a map of the
// locations which need to be remapped in higher level
// data structures (watch lists, etc).
//
// the returned map behaves as follows:
//
// 1. remap every removed clause in rm to CNull
// 2. remap every moved clause
//
func (c *CDat) Compact(rm []z.C) (map[z.C]z.C, int) {
	if len(rm) == 0 {
		return make(map[z.C]z.C, 0), 0
	}
	locMap := make(map[z.C]z.C, c.estimateLocMapSize(rm[0]))
	i := 0
	locMap[rm[i]] = CNull
	dst := rm[i] - 1
	cur := c.Next(rm[i])
	end := z.C(c.Len)
	var nrm z.C
	D := c.D
	for cur < end {
		if i+1 < len(rm) {
			nrm = rm[i+1]
		} else {
			nrm = CNull
		}
		if cur == nrm {
			// next to remove, skip copy
			locMap[cur] = CNull
			cur = c.Next(cur)
			i++
			continue
		}
		nxt := c.Next(cur)
		curLen := (nxt - cur)
		nxtDst := dst + curLen

		copy(D[dst:nxtDst], D[cur-1:nxt-1])
		locMap[cur] = dst + 1

		dst = nxtDst
		cur = nxt
	}
	freed := c.Len - int(dst)
	c.Len = int(dst)
	c.ClsLen -= len(rm)
	return locMap, freed
}

// Bump increases a score for the clause with location p.
func (c *CDat) Bump(p z.C) bool {
	h := c.Chd(p)
	b, decay := h.Bump(c.bumpInc)
	c.SetChd(p, b)
	return decay
}

func (c *CDat) Decay() {
	c.bumpInc += 5
}

// Function Load loads a clause with location p to the
// lit slice ms
func (c *CDat) Load(p z.C, ms []z.Lit) []z.Lit {
	e := c.Next(p) - 2
	ms = append(ms, c.D[p:e]...)
	return ms
}

// Func Forall applies f to every clause in the store.
func (c *CDat) Forall(f func(i int, p z.C, ms []z.Lit)) {
	ns := make([]z.Lit, 0, c.ClsLen*2/c.Len)
	i := 0
	q := z.C(1)
	for q < z.C(c.Len) {
		ns = ns[:0]
		// nb c.Load also calls next, maybe we can optimise this.
		ns = c.Load(q, ns)
		f(i, q, ns)
		q = c.Next(q)
		i++
	}
}

// func Dimacs writes a dimacs file (no header)
func (c *CDat) Dimacs(w io.Writer) error {
	var ret error
	c.Forall(func(i int, o z.C, ms []z.Lit) {
		if ret != nil {
			return
		}
		//fmt.Fprintf(w, "%s: ", o)
		for _, m := range ms {
			_, e := fmt.Fprintf(w, "%d ", m.Dimacs())
			if e != nil {
				ret = e
				return
			}
		}
		_, e := fmt.Fprintf(w, "0\n")
		if e != nil {
			ret = e
		}
	})
	return ret
}

// Copy makes a copy of c.
func (c *CDat) Copy() *CDat {
	other := &CDat{}
	c.CopyTo(other)
	return other
}

func (c *CDat) CopyTo(other *CDat) {
	other.D = make([]z.Lit, len(c.D), cap(c.D))
	other.Len = c.Len
	other.Cap = c.Cap
	other.ClsLen = c.ClsLen
	copy(other.D, c.D)
}

// Dimacs representation
func (c *CDat) String() string {
	buf := bytes.NewBuffer(nil)
	c.Dimacs(buf)
	return string(buf.Bytes())
}

// conservatively estimate location remap size based on average clause length
// and offset of least clause to remove.
func (c *CDat) estimateLocMapSize(loc z.C) int {
	sufLen := c.Len - int(loc)
	avgLen := float64(sufLen) / float64(c.ClsLen)
	u := int(math.Ceil(avgLen))
	return u + 128
}

func (c *CDat) grow(rLen int) {
	newCap := c.Cap
	for newCap <= rLen {
		newCap *= 2
	}
	d := make([]z.Lit, newCap)
	copy(d, c.D[:c.Len])
	c.D = d
	c.Cap = newCap
}

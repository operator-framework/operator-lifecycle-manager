// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package xo

import "fmt"

// A lot of info packed into 32 bit header
type Chd uint32

const (
	szBits    = 5
	lbdBits   = 4
	heatBits  = 22
	heatShift = szBits + lbdBits
)

const (
	szMask   uint32 = (1 << szBits) - 1
	lbdMask         = 0xf << szBits
	heatMask        = ((1 << heatBits) - 1) << (szBits + lbdBits)
	lrnMask         = 1 << 31
)

func MakeChd(learnt bool, lbd, sz int) Chd {
	v := uint32(0)
	if learnt {
		v |= lrnMask
	}
	v |= (uint32(sz) & szMask)
	v |= (uint32(lbd) << szBits) & lbdMask
	return Chd(v)
}

func (c Chd) Size() uint32 {
	return uint32(c) & szMask
}

func (c Chd) Lbd() uint32 {
	return uint32((c & lbdMask) >> szBits)
}

func (c Chd) Learnt() bool {
	return c >= lrnMask
}

func (c Chd) Heat() uint32 {
	return (uint32(c) & heatMask) >> heatShift
}

func (c Chd) Bump(n uint32) (Chd, bool) {
	ht := c.Heat() + n
	return Chd((uint32(c) & (lrnMask | szMask | lbdMask)) | (ht << heatShift)), ht == heatMask>>heatShift
}

func (c Chd) Decay() Chd {
	ht := c.Heat() / 2
	return Chd((uint32(c) & (lrnMask | szMask | lbdMask)) | (ht << heatShift))
}

func (c Chd) String() string {
	var l string
	if c.Learnt() {
		l = "*"
	} else {
		l = "a"
	}
	return fmt.Sprintf("c[lbd:%d, learnt:%s, size:%d, heat:%d]", c.Lbd(), l, c.Size(), c.Heat())
}

// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package xo

type Luby struct {
	exp   uint
	turns uint
}

func NewLuby() *Luby {
	return &Luby{exp: 0, turns: 0}
}

func (l *Luby) Next() uint {
	res := uint(1 << l.exp)
	if res&l.turns == 0 {
		l.exp = 0
		l.turns++
	} else {
		l.exp++
	}
	return res
}

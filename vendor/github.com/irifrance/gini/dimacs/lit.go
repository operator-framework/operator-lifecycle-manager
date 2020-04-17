// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package dimacs

import (
	"bufio"
	"github.com/irifrance/gini/z"
)

func readLit(r *bufio.Reader) (m z.Lit, e error) {
	var i int
	i, e = readInt(r)
	if e != nil {
		return
	}
	m = z.Dimacs2Lit(i)
	return
}

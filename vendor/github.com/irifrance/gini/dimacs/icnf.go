// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package dimacs

import (
	"bufio"
	"fmt"
	"github.com/irifrance/gini/z"
	"io"
)

type iCnfReader struct {
	rdr *bufio.Reader
	vis ICnfVis
}

// ReadICnf reads an icnf file which is a file format
// for incremental sat solving.
func ReadICnf(r io.Reader, vis ICnfVis) error {
	cfiltRdr := NewCommentFilter(r)
	iRdr := &iCnfReader{
		rdr: bufio.NewReader(cfiltRdr),
		vis: vis}
	return iRdr.read()
}

func (i *iCnfReader) read() error {
	if e := i.readP(); e != nil {
		return e
	}
	rdr := i.rdr
	vis := i.vis
	assumptions := false
	for {
		c, e := rdr.ReadByte()
		if e == io.EOF {
			vis.Eof()
			return nil
		}
		if e != nil {
			return e
		}
		if c == 'a' {
			assumptions = true
		} else {
			if e := rdr.UnreadByte(); e != nil {
				return e
			}
		}
		m, e := readLit(rdr)
		if e != nil {
			return e
		}
		if assumptions {
			vis.Assume(m)
		} else {
			vis.Add(m)
		}
		if m == z.LitNull {
			assumptions = false
		}
	}
}

func (i *iCnfReader) readP() error {
	expect := []byte("p inccnf\n")
	m := len(expect)
	rdr := i.rdr
	for i := 0; i < m; i++ {
		c, e := rdr.ReadByte()
		if e != nil {
			return e
		}
		if c != expect[i] {
			return fmt.Errorf("unexpected: '%c'\n", c)
		}
	}
	return nil
}

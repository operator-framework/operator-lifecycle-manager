// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package dimacs

import (
	"bufio"
	"fmt"
	"github.com/irifrance/gini/z"
	"io"
)

// Type Reader holds info for reading dimacs formatted intput.
type cnfReader struct {
	rdr     *bufio.Reader
	vis     CnfVis
	vMax    int
	nCls    int
	hdrVars int
	hdrCls  int
	strict  bool
	last    byte
}

// NewDimacsReader creates a new object for reading dimacs files.
func newCnfReader(r io.Reader, vis CnfVis) *cnfReader {
	cfiltRdr := NewCommentFilter(r)
	return &cnfReader{
		rdr:     bufio.NewReader(cfiltRdr),
		vis:     vis,
		vMax:    0,
		nCls:    0,
		hdrVars: -1,
		hdrCls:  -1}
}

// ReadCnf reads a dimacs CNF outputing the info to vis.
func ReadCnf(r io.Reader, vis CnfVis) error {
	return ReadCnfStrict(r, vis, false)
}

// ReadCnfStrict reads a dimacs CNF file with strict (count of vars/clauses in header)
// specified.
func ReadCnfStrict(r io.Reader, vis CnfVis, strict bool) error {
	cnfRdr := newCnfReader(r, vis)
	cnfRdr.SetStrict(strict)
	return cnfRdr.Read()
}

// Sets whether to enforce problem statement is
// present and accurate.  By default, this is false.
// However, a malformed problem statement will still
// result in a parsing error even if strict is false.
func (r *cnfReader) SetStrict(v bool) {
	r.strict = v
}

// Read parses a dimacs formatted cnf problem
func (r *cnfReader) Read() error {
	e := r.readHeader()
	if e != nil {
		if r.strict || e != io.EOF {
			return e
		}
	}
	if r.strict {
		if r.hdrVars == -1 || r.hdrCls == -1 {
			return fmt.Errorf("no header specified\n")
		}
	}
	e = r.readBody()
	if e != nil && e != io.EOF {
		return e
	}
	if r.strict {
		if r.hdrVars != r.vMax || r.hdrCls != r.nCls {
			return fmt.Errorf("wrong number of vars/clauses in header %d:%d != %d:%d",
				r.hdrVars, r.hdrCls, r.vMax, r.nCls)
		}
	}
	return nil
}

// ReadHeaders reads the header 'p cnf N M'
func (r *cnfReader) readHeader() error {
	var e error = nil
	var b byte
	b, e = r.rdr.ReadByte()
	if e != nil {
		return e
	}
	if b == byte('p') {
		if e := r.rdr.UnreadByte(); e != nil {
			return e
		}
		return r.readP()
	}
	return r.rdr.UnreadByte()
}

func (r *cnfReader) readBody() error {
	vCap := r.hdrVars
	if vCap == -1 {
		vCap = 8192
	}
	cCap := r.hdrCls
	if cCap == -1 {
		cCap = int(vCap) * 5
	}
	r.vis.Init(vCap, cCap)
	v := 0
	var e error
	vis := r.vis
	for {
		v, e = readInt(r.rdr)
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		if v == 0 {
			vis.Add(0)
			r.nCls++
			continue
		}
		if v < 0 {
			if -v > r.vMax {
				r.vMax = -v
			}
		} else if r.vMax < v {
			r.vMax = v
		}
		vis.Add(z.Dimacs2Lit(v))
	}
}

// called after 'p' at beginning of line
func (r *cnfReader) readP() error {
	if r.hdrVars != -1 {
		return fmt.Errorf("more than one problem statement\n")
	}
	rdr := r.rdr
	for _, c := range []byte{byte('p'), byte(' '), byte('c'), byte('n'), byte('f'), byte(' ')} {
		b, e := rdr.ReadByte()
		if e != nil {
			return e
		}
		if b != c {
			return fmt.Errorf("problem statement: expected '%c' got '%c'\n", c, b)
		}
	}
	nv, e := readInt(rdr)
	if e != nil {
		return e
	}
	nc, e := readInt(rdr)
	if e != nil {
		return e
	}
	r.hdrVars = nv
	r.hdrCls = nc
	return nil
}

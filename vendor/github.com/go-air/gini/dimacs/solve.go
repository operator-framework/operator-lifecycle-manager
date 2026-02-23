// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package dimacs

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

func ReadSolve(r io.Reader, vis SolveVis) error {
	cfiltRdr := NewCommentFilter(r)
	s := &sRdr{
		rdr: bufio.NewReader(cfiltRdr),
		vis: vis}
	return s.read()
}

type solveState int

const (
	neither solveState = iota
	sol
	values
)

type sRdr struct {
	rdr *bufio.Reader
	vis SolveVis
}

func (s *sRdr) handleSolution(sol string) error {
	sol = strings.TrimSpace(sol)
	switch sol {
	case "SATISFIABLE":
		s.vis.Solution(1)
	case "UNSATISFIABLE":
		s.vis.Solution(-1)
	case "UNKNOWN":
		s.vis.Solution(0)
	default:
		return fmt.Errorf("unknown solution '%s", sol)
	}
	return nil
}

func (s *sRdr) read() error {
	rdr := s.rdr
	vis := s.vis
	state := neither
	solbuf := make([]byte, 0, 32)
	line := 0
	for {
		c, e := rdr.ReadByte()
		if e == io.EOF {
			if len(solbuf) != 0 {
				if e := s.handleSolution(string(solbuf)); e != nil {
					return e
				}
			}
			vis.Eof()
			return nil
		}
		if e != nil {
			return e
		}
		if c == byte('\n') {
			line++
			state = neither
			if len(solbuf) != 0 {
				if e := s.handleSolution(string(solbuf)); e != nil {
					return e
				}
			}
			solbuf = solbuf[:0]
			continue
		}
		switch state {
		case neither:
			if c == byte('s') {
				state = sol
			} else if c == byte('v') {
				state = values
			} else {
				return fmt.Errorf("unexpected character '%c' at line %d\n", c, line)
			}
		case values:
			if e := rdr.UnreadByte(); e != nil {
				return e
			}
			m, e := readLit(rdr)
			if e != nil {
				return e
			}
			vis.Value(m)
		case sol:
			solbuf = append(solbuf, c)
		}
	}
}

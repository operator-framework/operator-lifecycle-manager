// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package dimacs

import "io"

// CommentFilter is a wrapper around reader which filters out
// dimacs comments.
type CommentFilter struct {
	rdr   io.Reader
	state state
}

func NewCommentFilter(r io.Reader) io.Reader {
	return &CommentFilter{rdr: r, state: start}
}

type state int

const (
	inComment state = iota
	newLine
	newLineC
	notComment
	start
)

func (s state) String() string {
	switch s {
	case inComment:
		return "c"
	case newLine:
		return "n"
	case notComment:
		return "-"
	case start:
		return "s"
	default:
		panic("unreachable")
	}
}

func (f *CommentFilter) Read(buf []byte) (n int, e error) {
Start:
	n, e = f.rdr.Read(buf)
	if e != nil {
		return n, e
	}
	j := 0
	state := f.state
	for _, b := range buf[:n] {
		switch state {
		case inComment:
			if b == byte('\n') {
				state = newLineC
			}
		case notComment:
			buf[j] = b
			j++
			if b == byte('\n') {
				state = newLine
				continue
			}
		case newLineC:
			if b == byte('c') {
				state = inComment
				continue
			}
			buf[j] = b
			j++
			state = notComment
		case newLine:
			if b == byte('c') {
				state = inComment
				continue
			}
			buf[j] = b
			j++
			if b != byte('\n') {
				state = notComment
			}
		case start:
			if b == byte('c') {
				state = inComment
			} else if b == byte('\n') {
				state = newLine
				buf[j] = b
				j++
			} else {
				state = notComment
				buf[j] = b
				j++
			}
		}
	}
	f.state = state
	if j == 0 {
		goto Start
	}
	return j, nil
}

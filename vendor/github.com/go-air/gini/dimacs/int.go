// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package dimacs

import (
	"bufio"
	"fmt"
	"io"
)

func readInt(rdr *bufio.Reader) (int, error) {
	v := 0
	m := 1
	c, e := readNextNonWhiteByte(rdr)

	if e != nil && e != io.EOF {
		return 0, e
	}
	if e == io.EOF && c == 0 {
		return 0, e
	}
	if c >= byte('0') && c <= byte('9') {
		v = int(c - byte('0'))
	} else if c == byte('-') {
		m = -1
	} else {
		return 0, fmt.Errorf("bad character for int: %c", c)
	}
	for {
		c, e = rdr.ReadByte()
		if e == io.EOF {
			return v * m, nil
		}
		if e != nil {
			return 0, e
		}
		if c == byte(' ') || c == byte('\n') || c == byte('\t') || c == byte('\r') {
			break
		}
		if c < byte('0') || c > byte('9') {
			return 0, fmt.Errorf("bad character for int: %c", c)
		}
		v *= 10
		v += int(c - byte('0'))
	}
	return v * m, nil
}

func readNextNonWhiteByte(rdr *bufio.Reader) (byte, error) {
	var c byte
	var e error
	for {
		c, e = rdr.ReadByte()
		if e != nil {
			return 0, e
		}
		if c == byte(' ') || c == byte('\n') || c == byte('\t') || c == byte('\r') {
			continue
		}
		return c, nil
	}
}

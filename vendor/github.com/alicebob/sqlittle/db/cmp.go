// docs:
// https://sqlite.org/datatype3.html chapter "4. Comparison Expressions"

package db

import (
	"bytes"
	"strings"
)

type collate func(string, string) int

var DefaultCollate = "binary"

// available collate functions
var CollateFuncs = map[string]func(string, string) int{
	"binary": strings.Compare,
	"rtrim": func(a, b string) int {
		return strings.Compare(
			strings.TrimRight(a, " \t\r\n"),
			strings.TrimRight(b, " \t\r\n"),
		)
	},
	"nocase": func(a, b string) int {
		lc := func(r rune) rune {
			if r >= 'A' && r <= 'Z' {
				return rune(strings.ToLower(string(r))[0])
			}
			return r
		}
		return strings.Compare(
			strings.Map(lc, a),
			strings.Map(lc, b),
		)
	},
}

type Key []KeyCol

type KeyCol struct {
	V       interface{}
	Collate string // either empty or names a valid CollateFuncs key
	Desc    bool
}

func Equals(key Key, r Record) bool {
	for i, k := range key {
		if len(r)-1 < i {
			return false
		}
		coll := DefaultCollate
		if k.Collate != "" {
			coll = k.Collate
		}
		if compare(k.V, r[i], CollateFuncs[coll]) != 0 {
			return false
		}
	}
	return true
}

// True if r is eq or bigger than key
func Search(key Key, r Record) bool {
	for i, k := range key {
		if len(r)-1 < i {
			return false
		}
		coll := DefaultCollate
		if k.Collate != "" {
			coll = k.Collate
		}
		cmp := compare(k.V, r[i], CollateFuncs[coll])
		if k.Desc {
			switch {
			case cmp > 0:
				return true
			case cmp == 0:
			default:
				return false
			}
		} else {
			switch {
			case cmp < 0:
				return true
			case cmp == 0:
			case cmp > 0:
				return false
			}
		}
	}
	return true
}

// compare record values, with ordering according to SQLite's type sort order:
//    nil < {int64|float64} < string < []byte
//
// same logic as strings.Compare:
// returns:
//   -1 when a is smaller than b
//   0 when all fields from a match b
//   1 when a is bigger than b
func compare(a, b interface{}, c collate) int {
	switch at := a.(type) {
	case nil:
		switch b.(type) {
		case nil:
			return 0
		case int64, float64, string, []byte:
			return -1
		default:
			panic("impossible cmp type")
		}
	case int64:
		switch bt := b.(type) {
		case nil:
			return 1
		case int64:
			return cmpInt64(at, bt)
		case float64:
			return cmpFloat64(float64(at), bt)
		case string, []byte:
			return -1
		default:
			panic("impossible cmp type")
		}
	case float64:
		switch bt := b.(type) {
		case nil:
			return 1
		case int64:
			return cmpFloat64(at, float64(bt))
		case float64:
			return cmpFloat64(at, bt)
		case string, []byte:
			return -1
		default:
			panic("impossible cmp type")
		}
	case string:
		switch bt := b.(type) {
		case nil, int64, float64:
			return 1
		case string:
			return c(at, bt)
		case []byte:
			return -1
		default:
			panic("impossible cmp type")
		}
	case []byte:
		switch bt := b.(type) {
		case nil, int64, float64, string:
			return 1
		case []byte:
			return bytes.Compare(at, bt)
		default:
			panic("impossible cmp type")
		}

	default:
		panic("impossible cmp type for a")
	}
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a == b:
		return 0
	default:
		return 1
	}
}

func cmpFloat64(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a == b:
		return 0
	default:
		return 1
	}
}

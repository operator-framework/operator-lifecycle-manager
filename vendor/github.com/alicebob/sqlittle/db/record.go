package db

import (
	"encoding/binary"
	"errors"
	"math"
)

var (
	errInternal = errors.New("unexpected record type found")
)

// Record is a row in the database.
// It can only have fields of these types: nil, int64, float64, string, []byte
type Record []interface{}

func parseRecord(r []byte) (Record, error) {
	var res Record
	hSize, n := readVarint(r)
	if n < 0 || hSize < int64(n) || hSize > int64(len(r)) {
		return res, ErrCorrupted
	}
	header, body := r[n:hSize], r[hSize:]
	for len(header) > 0 {
		c, n := readVarint(header)
		if n < 0 {
			return res, ErrCorrupted
		}
		header = header[n:]
		switch c {
		case 0:
			// NULL
			res = append(res, nil)
		case 1:
			// 8-bit twos-complement integer.
			if len(body) < 1 {
				return res, ErrCorrupted
			}
			res = append(res, int64(int8(body[0])))
			body = body[1:]
		case 2:
			// Value is a big-endian 16-bit twos-complement integer.
			if len(body) < 2 {
				return res, ErrCorrupted
			}
			res = append(res, int64(int16(binary.BigEndian.Uint16(body[:2]))))
			body = body[2:]
		case 3:
			// Value is a big-endian 24-bit twos-complement integer.
			if len(body) < 3 {
				return res, ErrCorrupted
			}
			res = append(res, readTwos24(body))
			body = body[3:]
		case 4:
			// Value is a big-endian 32-bit twos-complement integer.
			if len(body) < 4 {
				return res, ErrCorrupted
			}
			res = append(res, int64(int32(binary.BigEndian.Uint32(body[:4]))))
			body = body[4:]
		case 5:
			// Value is a big-endian 48-bit twos-complement integer.
			if len(body) < 6 {
				return res, ErrCorrupted
			}
			res = append(res, readTwos48(body))
			body = body[6:]
		case 6:
			// Value is a big-endian 64-bit twos-complement integer.
			if len(body) < 8 {
				return res, ErrCorrupted
			}
			res = append(res, int64(binary.BigEndian.Uint64(body[:8])))
			body = body[8:]
		case 7:
			// Value is a big-endian IEEE 754-2008 64-bit floating point number.
			if len(body) < 8 {
				return res, ErrCorrupted
			}
			res = append(res, math.Float64frombits(binary.BigEndian.Uint64(body[:8])))
			body = body[8:]
		case 8:
			// Value is the integer 0. (Only available for schema format 4 and higher.)
			res = append(res, int64(0))
		case 9:
			// Value is the integer 1. (Only available for schema format 4 and higher.)
			res = append(res, int64(1))
		case 10, 11:
			// internal types. Should not happen.
			return nil, errInternal
		default:
			if c&1 == 0 {
				// even, blob
				l := (c - 12) / 2
				if int64(len(body)) < l {
					return res, ErrCorrupted
				}
				p := body[:l]
				body = body[l:]
				res = append(res, p)
			} else {
				// odd, string
				// TODO: deal with encoding
				l := (c - 13) / 2
				if int64(len(body)) < l {
					return res, ErrCorrupted
				}
				p := body[:l]
				body = body[l:]
				res = append(res, string(p))
			}
		}
	}
	return res, nil
}

// Removes the rowid column from an index value (that's the last value from a
// Record).
// Returns: rowid, record, error
func ChompRowid(rec Record) (int64, Record, error) {
	if len(rec) == 0 {
		return 0, nil, errors.New("no fields in index")
	}
	rowid, ok := rec[len(rec)-1].(int64)
	if !ok {
		return 0, nil, errors.New("invalid rowid pointer in index")
	}
	rec = rec[:len(rec)-1]
	return rowid, rec, nil
}

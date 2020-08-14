// all public low level methods

package db

import (
	"errors"
	"fmt"

	"github.com/alicebob/sqlittle/sql"
)

type Table struct {
	db   *Database
	root int
	sql  string
}

// Index is either an index, or the table for a non-rowid table
type Index struct {
	db   *Database
	root int
	sql  string
}

// TableScanCB is the callback for Table.Scan(). It gets the rowid (usually an
// internal number), and the data of the row. It should return true when the
// scan should be terminated.
type TableScanCB func(int64, Record) bool

// RecordCB is passed to Index.Scan(), Index.ScanMin(), and
// Table.ScanWithoutRowid.
// It should return true when the scan should be stopped.
//
// For index scans:
// The callback gets the raw values as stored in the index. For a normal index
// the last value is the rowid value (see ChompRowid()). For a WITHOUT ROWID it
// depends on the table which rows there are.
type RecordCB func(Record) bool

// Def returns the table definition. Not everything SQLite supports is
// supported (yet).
// See Database.Schema() for a friendlier interface.
func (t *Table) Def() (*sql.CreateTableStmt, error) {
	c, err := sql.Parse(t.sql)
	if err != nil {
		return nil, fmt.Errorf("%s SQL: %q", err, t.sql)
	}
	stmt, ok := c.(sql.CreateTableStmt)
	if !ok {
		return nil, errors.New("no CREATE TABLE attached")
	}
	return &stmt, nil
}

// Scan calls cb() for every row in the table. Will be called in 'database
// order'.
// The record is given as sqlite stores it; this means:
//  - float64 columns might be stored as int64
//  - after an alter table which adds columns a row might miss the new columns
//  - an "integer primary key" column will be always be nil, and the rowid is
//  the value
// If the callback returns true (done) the scan will be stopped.
func (t *Table) Scan(cb TableScanCB) error {
	root, err := t.db.openTable(t.root)
	if err != nil {
		return err
	}
	_, err = root.Iter(
		maxRecursion,
		t.db,
		func(rowid int64, pl cellPayload) (bool, error) {
			c, err := addOverflow(t.db, pl)
			if err != nil {
				return false, err
			}

			rec, err := parseRecord(c)
			if err != nil {
				return false, err
			}
			return cb(rowid, rec), nil
		},
	)
	return err
}

// Rowid finds a single row by rowid. Will return nil if it isn't found.
// The rowid is an internal id, but if you have an `integer primary key` column
// that should be the same.
// See Table.Scan comments about the Record
func (t *Table) Rowid(rowid int64) (Record, error) {
	root, err := t.db.openTable(t.root)
	if err != nil {
		return nil, err
	}

	var recPl *cellPayload
	if _, err := root.IterMin(
		maxRecursion,
		t.db,
		rowid,
		func(k int64, pl cellPayload) (bool, error) {
			if k == rowid {
				recPl = &pl
			}
			return true, nil
		},
	); err != nil {
		return nil, err
	}
	if recPl == nil {
		return nil, nil
	}

	c, err := addOverflow(t.db, *recPl)
	if err != nil {
		return nil, err
	}
	return parseRecord(c)
}

// Def returns the index definition.
func (t *Index) Def() (*sql.CreateIndexStmt, error) {
	c, err := sql.Parse(t.sql)
	if err != nil {
		return nil, fmt.Errorf("%s SQL: %q", err, t.sql)
	}
	stmt, ok := c.(sql.CreateIndexStmt)
	if !ok {
		return nil, errors.New("no CREATE INDEX attached")
	}
	return &stmt, nil
}

// Scan calls cb() for every row in the index. These will be called in the
// index order.
// The callback gets the record as stored in the index. For indexes on a
// non-WITHOUT ROWID table the last value will be the rowid (see ChompRowid()).
// For a WITHOUT ROWID table the columns depend on your table structure.
// If the callback returns true (done) the scan will be stopped.
func (in *Index) Scan(cb RecordCB) error {
	root, err := in.db.openIndex(in.root)
	if err != nil {
		return err
	}

	_, err = root.Iter(
		maxRecursion,
		in.db,
		func(rec Record) (bool, error) {
			return cb(rec), nil
		},
	)
	return err
}

// Scan all record matching key
func (in *Index) ScanEq(key Key, cb RecordCB) error {
	root, err := in.db.openIndex(in.root)
	if err != nil {
		return err
	}

	_, err = root.IterMin(
		maxRecursion,
		in.db,
		key,
		func(rec Record) (bool, error) {
			if !Equals(key, rec) {
				return true, nil
			}
			return cb(rec), nil
		},
	)
	return err
}

// ScanMin calls cb() for every row in the index, starting from the first
// record where key is true.
//
// If the callback returns true (done) the scan will be stopped.
// All comments from Index.Scan are valid here as well.
func (in *Index) ScanMin(from Key, cb RecordCB) error {
	root, err := in.db.openIndex(in.root)
	if err != nil {
		return err
	}

	_, err = root.IterMin(
		maxRecursion,
		in.db,
		from,
		func(rec Record) (bool, error) {
			return cb(rec), nil
		},
	)
	return err
}

// Find all records where from(index) is true, and to(index) is false.
//
// You'll have to compensate for DESC columns.
//
func (in *Index) ScanRange(from, to Key, cb RecordCB) error {
	root, err := in.db.openIndex(in.root)
	if err != nil {
		return err
	}

	_, err = root.IterMin(
		maxRecursion,
		in.db,
		from,
		func(rec Record) (bool, error) {
			if Search(to, rec) {
				return true, nil
			}
			return cb(rec), nil
		},
	)
	return err
}

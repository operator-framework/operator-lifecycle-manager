package sqlittle

import (
	"errors"
	"fmt"

	sdb "github.com/alicebob/sqlittle/db"
)

type DB struct {
	db *sdb.Database
}

// Open a sqlite file. It can be concurrently written to by SQLite in other
// processes.
func Open(filename string) (*DB, error) {
	db, err := sdb.OpenFile(filename)
	if err != nil {
		return nil, err
	}
	return &DB{
		db: db,
	}, nil
}

// Close the database file
func (db *DB) Close() error {
	return db.db.Close()
}

// RowCB is the callback called for every matching row in the various
// select-like functions. Use `Scan()` on the `Row` argument to read row
// values.
type RowCB func(Row)

// Select the columns from every row from the given table. Order is the rowid
// order for rowid tables, and the ordered primary key for non-rowid tables
// (`WITHOUT ROWID`).
//
// For rowid tables the special values "rowid", "oid", and "_rowid_" will load
// the rowid (unless there is a column with that name).
func (db *DB) Select(table string, cb RowCB, columns ...string) error {
	if err := db.db.RLock(); err != nil {
		return err
	}
	defer db.db.RUnlock()

	s, err := db.db.Schema(table)
	if err != nil {
		return err
	}

	if s.WithoutRowid {
		return selectNonRowid(db.db, s, cb, columns)
	} else {
		return select_(db.db, s, cb, columns)
	}
}

// Select by rowid. Returns a nil row if the rowid isn't found.
// Returns an error on a non-rowid table ('WITHOUT ROWID').
func (db *DB) SelectRowid(table string, rowid int64, columns ...string) (Row, error) {
	if err := db.db.RLock(); err != nil {
		return nil, err
	}
	defer db.db.RUnlock()

	s, err := db.db.Schema(table)
	if err != nil {
		return nil, err
	}
	if s.WithoutRowid {
		return nil, errors.New("can't use SelectRowid on a WITHOUT ROWID table")
	}
	return selectRowid(db.db, s, rowid, columns)
}

// Select all rows from the given table via the index. The order will be the
// index order (every `DESC` field will iterate in descending order).
//
// `columns` are the name of the columns you want, their values always come
// from the data table. Index columns can have expressions, but that doesn't do
// anything (except maybe change the order).
//
// If the index has a WHERE expression only the rows matching that expression
// will be matched.
func (db *DB) IndexedSelect(table, index string, cb RowCB, columns ...string) error {
	if err := db.db.RLock(); err != nil {
		return err
	}
	defer db.db.RUnlock()

	s, err := db.db.Schema(table)
	if err != nil {
		return fmt.Errorf("schema err: %s", err)
	}

	ind := s.NamedIndex(index)
	if ind == nil {
		return fmt.Errorf("no such index: %q", index)
	}

	if s.WithoutRowid {
		return indexedSelectNonRowid(db.db, s, ind, cb, columns)
	} else {
		return indexedSelect(db.db, s, ind, cb, columns)
	}
}

// Select all rows matching key from the given table via the index. The order
// will be the index order (every `DESC` field will iterate in descending order).
// Any collate function defined in the schema will be applied automatically.
//
// `key` is compared against the index columns. `key` can have fewer columns than
// the index, in which case only the given columns need to compare equal.
// If the index column is an expression then `key` is compared against the
// value stored in the index.
//
// For example, given a table:
//    1: "aap", 1
//    2: "aap", 13
//    3: "noot", 12
// matches:
//    Key{"aap", 1} will match rows 1
//    Key{"aap"} will match rows 1 and 2
//    Key{"noot", 1} will not match any row
//    Key{} will match every row
//
// If the index has a WHERE expression only the rows matching that expression
// will be matched.
func (db *DB) IndexedSelectEq(table, index string, key Key, cb RowCB, columns ...string) error {
	if err := db.db.RLock(); err != nil {
		return err
	}
	defer db.db.RUnlock()

	s, err := db.db.Schema(table)
	if err != nil {
		return fmt.Errorf("schema err: %s", err)
	}

	ind := s.NamedIndex(index)
	if ind == nil {
		return fmt.Errorf("no such index: %q", index)
	}

	dbkey, err := asDbKey(key, ind.Columns)
	if err != nil {
		return err
	}

	if s.WithoutRowid {
		return indexedSelectEqNonRowid(db.db, s, ind, dbkey, cb, columns)
	} else {
		return indexedSelectEq(db.db, s, ind, dbkey, cb, columns)
	}
}

// Select rows via a Primary Key lookup.
//
// `key` is compared against the columns of the primary key. `key` can have fewer
// columns than the primary key has, in which case only the given columns need
// to compare equal (see `IndexedSelectEq` for an example).
// Any collate function defined in the schema will be applied automatically.
//
// PKSelect is especially efficient for non-rowid tables (`WITHOUT ROWID`), and
// for rowid tables which have a single 'integer primary key' column.
func (db *DB) PKSelect(table string, key Key, cb RowCB, columns ...string) error {
	if err := db.db.RLock(); err != nil {
		return err
	}
	defer db.db.RUnlock()

	s, err := db.db.Schema(table)
	if err != nil {
		return err
	}

	if s.WithoutRowid {
		return pkSelectNonRowid(db.db, s, key, cb, columns)
	} else {
		return pkSelect(db.db, s, key, cb, columns)
	}
}

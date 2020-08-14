package sqlittle

import (
	sdb "github.com/alicebob/sqlittle/db"
)

// index scan on a rowid table
func indexedSelect(
	db *sdb.Database,
	schema *sdb.Schema,
	index *sdb.SchemaIndex,
	cb RowCB,
	columns []string,
) error {
	ci, err := toColumnIndexRowid(schema, columns)
	if err != nil {
		return err
	}

	tab, err := db.Table(schema.Table)
	if err != nil {
		return err
	}

	ind, err := db.Index(index.Index)
	if err != nil {
		return err
	}

	return ind.Scan(func(r sdb.Record) bool {
		rowid, _, err := sdb.ChompRowid(r)
		if err != nil {
			return false
		}
		row, err := tab.Rowid(rowid)
		if err != nil || row == nil {
			// row should never be nil
			return false
		}
		cb(toRow(rowid, ci, row))
		return false
	})
}

// index (==) search on a rowid table
func indexedSelectEq(
	db *sdb.Database,
	schema *sdb.Schema,
	index *sdb.SchemaIndex,
	key sdb.Key,
	cb RowCB,
	columns []string,
) error {
	ci, err := toColumnIndexRowid(schema, columns)
	if err != nil {
		return err
	}

	tab, err := db.Table(schema.Table)
	if err != nil {
		return err
	}

	ind, err := db.Index(index.Index)
	if err != nil {
		return err
	}

	return ind.ScanEq(
		key,
		func(r sdb.Record) bool {
			rowid, _, err := sdb.ChompRowid(r)
			if err != nil {
				return false
			}
			row, err := tab.Rowid(rowid)
			if err != nil || row == nil {
				// row should never be nil
				return false
			}
			cb(toRow(rowid, ci, row))
			return false
		})
}

// index scan on a WITHOUT ROWID table
func indexedSelectNonRowid(
	db *sdb.Database,
	schema *sdb.Schema,
	index *sdb.SchemaIndex,
	cb RowCB,
	columns []string,
) error {
	ci, err := toColumnIndexNonRowid(schema, columns)
	if err != nil {
		return err
	}

	tab, err := db.NonRowidTable(schema.Table)
	if err != nil {
		return err
	}

	ind, err := db.Index(index.Index)
	if err != nil {
		return err
	}

	cols := pkColumns(schema, index)
	// make an empty key with the correct definition which we update in the
	// callback
	pk, err := asDbKey(make(Key, len(schema.PK)), schema.PK)
	if err != nil {
		return err
	}

	return ind.Scan(func(r sdb.Record) bool {
		setKey(r, cols, pk)

		var found sdb.Record
		err := tab.ScanEq(pk, func(row sdb.Record) bool {
			found = row
			return true
		})
		if err != nil || found == nil {
			// found should never be nil
			return false
		}
		cb(toRow(0, ci, found))
		return false
	})
}

// index (==) search on a WITHOUT ROWID table
func indexedSelectEqNonRowid(
	db *sdb.Database,
	schema *sdb.Schema,
	index *sdb.SchemaIndex,
	key sdb.Key,
	cb RowCB,
	columns []string,
) error {
	ci, err := toColumnIndexNonRowid(schema, columns)
	if err != nil {
		return err
	}

	tab, err := db.NonRowidTable(schema.Table)
	if err != nil {
		return err
	}

	ind, err := db.Index(index.Index)
	if err != nil {
		return err
	}

	cols := pkColumns(schema, index)
	// make an empty key with the correct definition which we update in the
	// callback
	pk, err := asDbKey(make(Key, len(schema.PK)), schema.PK)
	if err != nil {
		return err
	}

	return ind.ScanEq(
		key,
		func(r sdb.Record) bool {
			setKey(r, cols, pk)

			var found sdb.Record
			err := tab.ScanEq(pk, func(row sdb.Record) bool { found = row; return true })
			if err != nil || found == nil {
				// found should never be nil
				return false
			}
			cb(toRow(0, ci, found))
			return false
		},
	)
}

// make a key from columns from the record
// updates key
func setKey(r sdb.Record, indexes []int, key sdb.Key) {
	for i, v := range indexes {
		key[i].V = r[v]
	}
}

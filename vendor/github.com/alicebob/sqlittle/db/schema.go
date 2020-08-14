// Schema describes a table and all indexes on that table.
// Both indexes from the `CREATE TABLE` and from any relevant `CREATE INDEX`-es
// are processed.
// It knows the SQLite conventions how tables and indexes are used, such as the
// names for internal indexes, when a column is stored in the rowid, &c.

package db

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/alicebob/sqlittle/sql"
)

type Schema struct {
	Table        string
	WithoutRowid bool
	Columns      []TableColumn
	Indexes      []SchemaIndex
	PK           []IndexColumn // only set for non-rowid tables
	PrimaryKey   string        // only set for rowid tables: name of the index
	RowidPK      bool          // only set for rowid tables: whether we have a 'integer primary key' column, which means there is no separate index for the primary key
}

type TableColumn struct {
	Column  string
	Type    string // as given in the CREATE TABLE
	Null    bool
	Default interface{}
	Collate string
	Rowid   bool
}

type SchemaIndex struct {
	Index   string
	Columns []IndexColumn
}

type IndexColumn struct {
	Column     string
	Expression string
	Collate    string
	SortOrder  sql.SortOrder
}

func newSchema(table string, master []sqliteMaster) (*Schema, error) {
	var createSQL string
	n := strings.ToLower(table)
	for _, m := range master {
		if m.typ == "table" && m.name == n {
			createSQL = m.sql
			break
		}
	}
	if createSQL == "" {
		return nil, fmt.Errorf("no such table: %q", table)
	}

	t, err := sql.Parse(createSQL)
	if err != nil {
		return nil, err
	}
	ct, ok := t.(sql.CreateTableStmt)
	if !ok {
		return nil, errors.New("unsupported CREATE TABLE statement")
	}

	st := newCreateTable(ct)

	for _, m := range master {
		if m.typ == "index" && m.tblName == n && m.sql != "" {
			// silently ignore indexes we don't understand
			if t, err := sql.Parse(m.sql); err == nil {
				if ci, ok := t.(sql.CreateIndexStmt); ok {
					st.addCreateIndex(ci)
				}
			}
		}
	}

	return st, nil
}

// transform a `create table` statement into a Schema, which knows which
// indexes are used
func newCreateTable(ct sql.CreateTableStmt) *Schema {
	st := &Schema{
		Table:        ct.Table,
		WithoutRowid: ct.WithoutRowid,
	}
	autoindex := 1
	for _, c := range ct.Columns {
		col := TableColumn{
			Column:  c.Name,
			Type:    c.Type,
			Null:    c.Null,
			Default: c.Default,
			Collate: c.Collate,
			Rowid:   false,
		}
		if c.PrimaryKey {
			col.Rowid = (!ct.WithoutRowid) && isRowid(false, c.Type, c.PrimaryKeyDir)
			col.Null = !ct.WithoutRowid && c.Null // w/o rowid forces not null

			name := fmt.Sprintf("sqlite_autoindex_%s_%d", st.Table, autoindex)
			if ct.WithoutRowid {
				name = ""
			}
			if ct.WithoutRowid {
				// non-rowid primary keys have a special place
				st.setPK([]IndexColumn{
					{
						Column:    c.Name,
						SortOrder: c.PrimaryKeyDir,
					},
				})
				autoindex++
			} else {
				if col.Rowid {
					st.RowidPK = true
				} else if st.addIndex(
					true,
					name,
					[]IndexColumn{
						{
							Column:    c.Name,
							SortOrder: c.PrimaryKeyDir,
						},
					},
				) {
					autoindex++
				}
			}
		}
		if c.Unique {
			if st.addIndex(
				false,
				fmt.Sprintf("sqlite_autoindex_%s_%d", st.Table, autoindex),
				[]IndexColumn{
					{
						Column:    c.Name,
						SortOrder: sql.Asc,
					},
				},
			) {
				autoindex++
			}
		}
		st.Columns = append(st.Columns, col)
	}
constraint:
	for _, c := range ct.Constraints {
		switch c := c.(type) {
		case sql.TablePrimaryKey:
			if !ct.WithoutRowid && len(c.IndexedColumns) == 1 {
				// is this column an alias for the rowid?
				col := st.column(c.IndexedColumns[0].Column)
				if isRowid(true, col.Type, c.IndexedColumns[0].SortOrder) {
					col.Rowid = true
					st.RowidPK = true
					continue constraint
				}
			}
			if ct.WithoutRowid {
				for _, co := range c.IndexedColumns {
					st.column(co.Column).Null = false
				}
				st.setPK(st.toIndexColumns(c.IndexedColumns))
				autoindex++
				continue
			}
			name := fmt.Sprintf("sqlite_autoindex_%s_%d", st.Table, autoindex)
			if st.addIndex(true, name, st.toIndexColumns(c.IndexedColumns)) {
				autoindex++
			}
		case sql.TableUnique:
			name := fmt.Sprintf("sqlite_autoindex_%s_%d", st.Table, autoindex)
			if st.addIndex(false, name, st.toIndexColumns(c.IndexedColumns)) {
				autoindex++
			}
		}
	}

	return st
}

// add `CREATE INDEX` statement to a table
// Does not check for duplicate indexes.
func (st *Schema) addCreateIndex(ci sql.CreateIndexStmt) {
	st.Indexes = append(st.Indexes, SchemaIndex{
		Index:   ci.Index,
		Columns: st.toIndexColumns(ci.IndexedColumns),
	})
}

// change sql index columns to schema index column. Looks up defaults from the
// column definitions
func (st *Schema) toIndexColumns(ci []sql.IndexedColumn) []IndexColumn {
	var cs []IndexColumn
	for _, col := range ci {
		c := IndexColumn{
			Column:     col.Column,
			Expression: col.Expression,
			SortOrder:  col.SortOrder,
		}
		if col.Column != "" {
			// not an expression column
			base := st.column(col.Column)
			if base != nil {
				collate := base.Collate
				if col.Collate != "" {
					collate = col.Collate
				}
				c.Collate = collate
			}
		}
		cs = append(cs, c)
	}
	return cs
}

// add an index. This is a noop if an equivalent index already exists. Returns
// whether the indexed got added.
func (st *Schema) addIndex(pk bool, name string, cols []IndexColumn) bool {
	if reflect.DeepEqual(st.PK, cols) {
		return false
	}
	for _, ind := range st.Indexes {
		if reflect.DeepEqual(ind.Columns, cols) {
			if pk {
				st.PrimaryKey = ind.Index
			}
			return false
		}
	}
	st.Indexes = append(st.Indexes, SchemaIndex{
		Index:   name,
		Columns: cols,
	})
	if pk {
		st.PrimaryKey = name
	}
	return true
}

// sets the PK key (for non-rowid tables). Deletes any duplicate indexes.
func (st *Schema) setPK(cols []IndexColumn) {
	st.PK = cols
	for i, ind := range st.Indexes {
		if reflect.DeepEqual(ind.Columns, cols) {
			st.Indexes = append(st.Indexes[:i], st.Indexes[i+1:]...)
			if len(st.Indexes) == 0 {
				st.Indexes = nil // to make test diffs easier
			}
		}
	}
}

// Returns the index of the named column, or -1.
func (st *Schema) Column(name string) int {
	u := strings.ToLower(name)
	for i, col := range st.Columns {
		if strings.ToLower(col.Column) == u {
			return i
		}
	}
	return -1
}

func (st *Schema) column(name string) *TableColumn {
	n := st.Column(name)
	if n < 0 {
		return nil // you're asking for non-exising columns and for trouble
	}
	return &st.Columns[n]
}

// NamedIndex returns the index with the name (case insensitive)
func (st *Schema) NamedIndex(name string) *SchemaIndex {
	u := strings.ToUpper(name)
	for i, ind := range st.Indexes {
		if strings.ToUpper(ind.Index) == u {
			return &st.Indexes[i]
		}
	}
	return nil
}

// Returns the index of the named column, or -1.
func (si *SchemaIndex) Column(name string) int {
	u := strings.ToUpper(name)
	for i, col := range si.Columns {
		if strings.ToUpper(col.Column) == u {
			return i
		}
	}
	return -1
}

// A primary key can be an alias for the rowid iff:
//  - this is not a `WITHOUT ROWID` table (not tested here)
//  - it's a single column of type 'INTEGER'
//  - ASC and DESC are fine for table constraints:
//     CREATE TABLE foo (a integer, primary key (a DESC))
//    but in a column statement it only works with a ASC:
//     CREATE TABLE foo (a integer primary key)
//    invalid:
//     ~~CREATE TABLE foo (a integer primary key DESC)~~
// If the row is an alias for the rowid it won't be stored in the datatable;
// all values will be null.
// See https://sqlite.org/lang_createtable.html#rowid
func isRowid(tableConstraint bool, typ string, dir sql.SortOrder) bool {
	if strings.ToUpper(typ) != "INTEGER" {
		return false
	}
	return tableConstraint || dir == sql.Asc
}

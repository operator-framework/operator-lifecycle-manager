# Low level interface

The low level package (github.com/alicebob/sqlittle/db) deals with reading
datafiles, locking, and reading SQL schemas. It provide various routines to iterate
over table and index data, but it does not know how to connect an index to a
data table, or exactly which columns are available in an index.

## examples

See [godoc](https://godoc.org/github.com/alicebob/sqlittle/db) for all available
methods and examples, but the gist of a table scan is:

    db, _ := OpenFile("testdata/single.sqlite")
    defer db.Close()
    table, _ := db.Table("hello")
    table.Scan(func(rowid int64, rec Record) bool {
        fmt.Printf("row %d: %s\n", rowid, rec[0].(string))
        return false // we want all the rows
    })


Printing the columns:

    db, _ := OpenFile("testdata/single.sqlite")
    defer db.Close()
    schema, _ := db.Schema("words")
    fmt.Printf("columns:\n")
    for _, c := range schema.Columns {
        fmt.Printf(" - %q is a %s\n", c.Name, c.Type)
    }


## locks

If you somehow know that no-one will change the .sqlite file you don't have to
use locks. Otherwise sandwich your logic between database.RLock() and
database.RUnlock() calls. Any *Table or *Index pointer you have is invalid
after database.RUnlock().


# low level SQLite gotchas

The low level routines don't change any fields, they simply pass on how data is
stored in the database by SQLite. Notably that includes:
- float64 columns might be stored as int64
- after an alter table which adds columns a row might miss those new columns
- "integer primary key" columns will be always be stored as `nil` in a table,
  and the rowid should be used as the value
- string indexes are compared with a simple binary comparison, no collating
  functions are used. If a column uses any other collating function for strings
  you can't use the index.
- you need to know whether a table is a rowid or non-rowid table
- the order of columns in a non-rowid table does not need to match their
  definition order

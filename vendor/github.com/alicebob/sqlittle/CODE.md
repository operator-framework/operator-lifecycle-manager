## code structure

This document explains the layers of sqlittle.

The code is split in three packages:
- `db/` the low level routines which deal with files
- `sql/` SQL parser for `CREATE TABLE` and `CREATE INDEX` statements
- `/` the higher level routines to hide SQLite quirks.

This document is mostly about the low level package.

This is how the files work together, with the lowest level on the
bottom:

    +---------------------------------------+
    | sqlittle.go, select.go, row.go        |
    +===================+===================+
    | db/low.go         | db/schema.go      |
    +-------------------+-------------------+
    | db/database.go                        |
    +---------------------------------------+
    | db/btree.go                           |
    +---------------------------------------+
    | pager (db/pager.go, db/pager_unix.go) |
    +---------------------------------------+

    
### pager

An `.sqlite` file consist of a sequence of pages all of the same size. Page
size is between 512 and 64K.  The `Pager` reads pages from the .sqlite database
file. It also knows how to lock that file. It knows nothing about what's in the
pages.

The Go interface is `Pager{}`, which is implemented on `pager_unix.go`. There
is an alternative in-memory implementation in the Fuzz Test. Feel free to
create a pager_windows.go if you need windows support.

### btree

Both table data and indexes are stored in binary trees, which are stored in
pages. The btree code knows how to interpret the bytes in pages. The btree code
has very low level routines to iterate and search in tables and indexes.

### database

`Database` is the main struct, which connects the pager and the btree code. It
also knows where to find the `sqlite_master` table, which stores all table
definitions. Database also deals with the caching of pages.

### low

The routines in `low.go` are the public low level routines. They mostly wrap
the iteration routines from the btree code into something a bit more friendly. 

### schema

The table and indexed definitions are stored by SQLite as `CREATE TABLE ...`
and `CREATE INDEX ...` statements in the database file. `schema.go` uses the
SQL parser from the sql/ subdir to parse those statements, and interprets the
result the same way SQLite does.

With the result you could test whether a table matches what you think it does
when you use the low level scan routines. It could also be used to build more
flexible query code.

### high level

`/sqlittle.go` gives methods which hide most SQLite details. It mostly calls code
from `/select.go` and `/indexed_select.go`.

`/row.go` has Scan() to deal with data conversions.

`/sqlite.go` knows how columns are stored in indexes.

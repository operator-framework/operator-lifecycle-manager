Package SQLittle provides pure Go, read-only, access to SQLite (version 3) database
files.

## What
SQLittle reads SQLite3 tables and indexes. It iterates over tables, and
can search efficiently using indexes. SQLittle will deal with all SQLite
storage quirks, but otherwise it doesn't try to be smart; if you want to use
an index you have to give the name of the index.

There is no support for SQL, and if you want to do the most efficient joins
possible you'll have to use the low level code.

Based on https://sqlite.org/fileformat2.html and some SQLite source code reading.

## Why
This whole thing is mostly for fun. The normal SQLite libraries are perfectly great, and
there is no real need for this. However, since this library is pure Go
cross-compilation is much easier. Given the constraints a valid use-case would
for example be storing app configuration in read-only sqlite files.

## Docs
https://godoc.org/github.com/alicebob/sqlittle for the go doc and examples.

See [LOWLEVEL.md](LOWLEVEL.md) about the low level reader.
See [CODE.md](CODE.md) for an overview how the code is structured.

## Features
Things SQLittle can do:

```
- table scan in row order; table scan in index order; simple searches with use of (partial) indexes
- works on both rowid and non-rowid (`WITHOUT ROWID`) tables
- files can be used concurrently with sqlite (compatible locks)
- behaves nicely on corrupted database files (no panics)
- detects corrupt journal files
- hides all SQLite low level storage details
- DESC indexes are handled automatically
- Collate functions are used automatically
- indexes with expression (either in columns or as a `WHERE`) are (partially) supported
- Scan() to most Go datatypes, including `time.Time`
- Works on Linux, Mac OS, and Windows
```

Things SQLittle should do:

```
- add a helper to find indexes. That would be especially useful for the `sqlite_autoindex_...` indexes
- optimize loading when all requested columns are available in the index
- expose the locking so you can do bigger read transactions
```

Things SQLittle can not do:

```
- read-only
- only supports UTF8 strings
- no joins
- WAL files are not supported
- indexes are used for sorting, but there is no on-the-fly sorting
```

## Locks
SQLittle has a read-lock on the file during the whole execution of the
select-like functions. It's safe to update the database using SQLite while the
file is opened in SQLittle.

## Status
The current level of abstraction is likely the final one (that is: deal
with reading single tables; don't even try joins or SQL or query planning), but
the API might still change.



# Examples

## Basic SELECT
Code:

```
{
	db, err := sqlittle.Open("./testdata/music.sqlite")
	if err != nil {
		panic(err)
	}
	defer db.Close()
	db.Select("tracks", func(r sqlittle.Row) {
		var (
			name   string
			length int
		)
		_ = r.Scan(&name, &length)
		fmt.Printf("%s: %d seconds\n", name, length)
	}, "name", "length")
}
```
Output:

```
Drive My Car: 145 seconds
Norwegian Wood: 121 seconds
You Wont See Me: 198 seconds
Come Together: 259 seconds
Something: 182 seconds
Maxwells Silver Hammer: 207 seconds

```



## SELECT by primary key
Code:

```
{
	db, err := sqlittle.Open("./testdata/music.sqlite")
	if err != nil {
		panic(err)
	}
	defer db.Close()
	db.PKSelect("tracks", sqlittle.Key{4}, func(r sqlittle.Row) {
		name, _ := r.ScanString()
		fmt.Printf("%s\n", name)
	}, "name")
}
```
Output:

```
Come Together

```



# &c.

[![GoDoc](https://godoc.org/github.com/alicebob/sqlittle?status.svg)](https://godoc.org/github.com/alicebob/sqlittle)
[![Build Status](https://travis-ci.org/alicebob/sqlittle.png?branch=master)](https://travis-ci.org/alicebob/sqlittle)

`make fuzz` uses [go-fuzz](https://github.com/dvyukov/go-fuzz)

The README is generated with [autoreadme](https://github.com/jimmyfrasche/autoreadme)

See [sqlite2go](https://github.com/cznic/sqlite2go/) for another approach to pure Go SQLite

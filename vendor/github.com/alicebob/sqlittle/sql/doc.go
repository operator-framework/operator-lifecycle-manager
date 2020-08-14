/*
Package sql is a parser for the subset of SQL needed for SQLite's `CREATE
TABLE` and `CREATE INDEX` statements.

It deals with most of https://sqlite.org/lang_createtable.html and
https://sqlite.org/lang_createindex.html

It is used by sqlittle to read the table and index definitions embedded in
`.sqlite` files.
*/
package sql

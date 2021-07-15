package sqlite

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

// Open opens a connection to a sqlite db. It should be used everywhere instead of sql.Open so that foreign keys are
// ensured.
func Open(fileName string) (*sql.DB, error) {
	return sql.Open("sqlite3", EnableForeignKeys(fileName))
}

// Open opens a connection to a sqlite db. It is
func OpenReadOnly(fileName string) (*sql.DB, error) {
	return sql.Open("sqlite3", EnableImmutable(fileName))
}

// EnableForeignKeys appends the option to enable foreign keys on connections
// note that without this option, PRAGMAs about foreign keys will lie.
func EnableForeignKeys(fileName string) string {
	return "file:" + fileName + "?_foreign_keys=on"
}

// Immutable appends the option to mark the db immutable on connections
func EnableImmutable(fileName string) string {
	return "file:" + fileName + "?immutable=true"
}

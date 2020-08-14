package db

const (
	sqlitePendingByte  = 0x40000000
	sqliteReservedByte = sqlitePendingByte + 1
	sqliteSharedFirst  = sqlitePendingByte + 2
	sqliteSharedSize   = 510
)

type pager interface {
	// load a page from storage.
	page(n int, pagesize int) ([]byte, error)
	// as it says
	Close() error
	// read lock
	RLock() error
	// unlock read lock
	RUnlock() error
	// true if there is any 'RESERVED' lock on this file
	CheckReservedLock() (bool, error)
}

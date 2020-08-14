// windows implementation of the `pager` interface (the file reader) with LockFileEx
// locking

// references:
// winapi docs: https://docs.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-lockfileex
// sqlite3 winlock: https://github.com/sqlite/sqlite/blob/c398c65bee850b6b8f24a44852872a27f114535d/src/os_win.c#L3236

package db

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/exp/mmap"
	"golang.org/x/sys/windows"
)

const (
	reserved uint32 = 0
)

// LockState is used to represent the program's opinion of whether a file is locked or not
// this does not represent the lock state of the file - we can't distinguish between
// a file that is locked by another process and failing to acquire a lock
type LockState int

const (
	Unlocked LockState = iota
	Locked
)

type filePager struct {
	f     *os.File
	mm    *mmap.ReaderAt
	state LockState
}

func newFilePager(file string) (*filePager, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	mm, err := mmap.Open(file)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &filePager{
		f:     f,
		mm:    mm,
		state: Unlocked,
	}, nil
}

// pages start counting at 1
func (f *filePager) page(id int, pagesize int) ([]byte, error) {
	buf := make([]byte, pagesize)
	_, err := f.mm.ReadAt(buf[:], int64(id-1)*int64(pagesize))
	return buf, err
}

func (f *filePager) lock(start, len uint32) error {
	ol := new(windows.Overlapped)
	ol.Offset = start
	return windows.LockFileEx(windows.Handle(f.f.Fd()), windows.LOCKFILE_FAIL_IMMEDIATELY|windows.LOCKFILE_EXCLUSIVE_LOCK, reserved, len, 0, ol)
}

func (f *filePager) unlock(start, len uint32) error {
	ol := new(windows.Overlapped)
	ol.Offset = start
	return windows.UnlockFileEx(windows.Handle(f.f.Fd()), reserved, len, 0, ol)
}

func (f *filePager) RLock() error {
	// Set a 'SHARED' lock, following winLock() logic from sqlite3.c
	if f.state == Locked {
		return errors.New("trying to lock a locked lock") // panic?
	}

	// Try 3 times to get the pending lock.  This is needed to work
	// around problems caused by indexing and/or anti-virus software on
	// Windows systems.
	// If you are using this code as a model for alternative VFSes, do not
	// copy this retry logic.  It is a hack intended for Windows only.
	//
	// source: https://github.com/sqlite/sqlite/blob/c398c65bee850b6b8f24a44852872a27f114535d/src/os_win.c#L3280
	for i := 0; i < 3; i++ {
		err := f.lock(sqlitePendingByte, 1)
		if err == nil {
			break
		}
		if errors.Is(err, windows.ERROR_INVALID_HANDLE) {
			return fmt.Errorf("failed to get lock, invalid handle")
		}
		time.Sleep(time.Microsecond)
	}

	defer func() {
		// - drop the pending lock. No idea what to do with the error :/
		f.unlock(sqlitePendingByte, 1)
	}()

	// Get the read-lock
	if err := f.lock(sqliteSharedFirst, sqliteSharedSize); err != nil {
		return err
	}
	f.state = Locked
	return nil
}

func (f *filePager) RUnlock() error {
	if f.state == Unlocked {
		return errors.New("trying to unlock an unlocked lock") // panic?
	}
	if err := f.unlock(sqliteSharedFirst, sqliteSharedSize); err != nil {
		return err
	}
	f.state = Unlocked
	return nil
}

// True if there is a 'reserved' lock on the database, by any process.
func (f *filePager) CheckReservedLock() (bool, error) {
	// per SQLite's winCheckReservedLock()
	err := f.lock(sqliteReservedByte, 1)
	if err == nil {
		f.unlock(sqliteReservedByte, 1)
	}
	return err == nil, err
}

func (f *filePager) Close() error {
	f.f.Close()
	return f.mm.Close()
}

// rollback journal

package db

import (
	"bytes"
	"encoding/binary"
	"os"
)

const (
	journalHeader = 28
)

var (
	journalMagic = [...]byte{0xd9, 0xd5, 0x05, 0xf9, 0x20, 0xa1, 0x63, 0xd7}
)

// validJournal is true if there is a valid -journal file present.
// `file` should the name of the journal file.
func validJournal(file string) (bool, error) {
	fh, err := os.Open(file)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		// maybe it's a directory, or no read permission.
		return false, err
	}
	defer fh.Close()
	var b = [journalHeader]byte{}
	if n, err := fh.Read(b[:]); err != nil || n != journalHeader {
		// a zero length file is allowed
		return false, nil
	}

	jh := struct {
		Magic      [8]byte
		_          int32 // Page Count
		_          int32 // Nonce
		_          int32 // Initial page count
		SectorSize int32 // Disk sector size
	}{}
	if err := binary.Read(
		bytes.NewBuffer(b[:]),
		binary.BigEndian,
		&jh,
	); err != nil {
		return false, nil
	}

	if jh.Magic != journalMagic {
		return false, nil
	}

	if jh.SectorSize < 512 || jh.SectorSize > 1<<16 {
		// sanity check
		return false, nil
	}

	// file should have at least a single full journal page
	var zeros = make([]byte, jh.SectorSize-journalHeader)
	if n, err := fh.Read(zeros); err != nil || n != len(zeros) {
		return false, nil
	}
	return true, nil
}

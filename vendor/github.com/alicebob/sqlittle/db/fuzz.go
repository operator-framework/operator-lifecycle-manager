package db

// Fuzz routine for the go-fuzz test system
func Fuzz(data []byte) int {
	if err := fuzz(data); err != nil {
		return 0
	}
	return 1
}

func fuzz(data []byte) error {
	p := bytePager(data)
	db, err := newDatabase(&p, "")
	if err != nil {
		return err
	}
	tables, err := db.Tables()
	if err != nil {
		return err
	}
	for _, t := range tables {
		table, err := db.Table(t)
		if err != nil {
			return err
		}

		if err := table.Scan(
			func(rowid int64, rec Record) bool {
				return false
			},
		); err != nil {
			return err
		}

		if _, err := table.Rowid(42); err != nil {
			return err
		}

		if _, err := table.Def(); err != nil {
			return err
		}
	}

	indexes, err := db.Indexes()
	if err != nil {
		return err
	}
	for _, in := range indexes {
		index, err := db.Index(in)
		if err != nil {
			return err
		}

		if err := index.Scan(
			func(rec Record) bool {
				return false
			},
		); err != nil {
			return err
		}

		if err := index.ScanMin(
			Key{KeyCol{V: "q"}},
			func(rec Record) bool {
				return false
			},
		); err != nil {
			return err
		}

		if _, err := index.Def(); err != nil {
			return err
		}
	}
	return nil
}

type bytePager []byte

func (b *bytePager) page(n int, pagesize int) ([]byte, error) {
	x := pagesize * (n - 1)
	y := x + pagesize
	if x < 0 || y > len(*b) {
		return nil, ErrCorrupted
	}
	return (*b)[x:y], nil
}

func (b *bytePager) RLock() error                     { return nil }
func (b *bytePager) RUnlock() error                   { return nil }
func (b *bytePager) CheckReservedLock() (bool, error) { return false, nil }
func (b *bytePager) Close() error                     { return nil }

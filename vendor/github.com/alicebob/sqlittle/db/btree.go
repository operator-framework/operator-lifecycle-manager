// Btree page types.
// 'Table' are data tables. InteriorTable pages have no data, and
// points to other pages. InteriorLeaf pages have data and don't point to other
// pages.
// 'Index' tables have index keys. Both the internal and leaf pages contain
// keys.

package db

import (
	"encoding/binary"
	"errors"
	"sort"
)

const (
	// "Define the depth of a leaf b-tree to be 1 and the depth of any interior
	// b-tree to be one more than the maximum depth of any of its children. In
	// a well-formed database, all children of an interior b-tree have the same
	// depth."
	// and:
	// "A "pointer" in an interior b-tree page is just the 31-bit integer page
	// number of the child page."
	// Ergo, in a well-formed database where every interal page only links to a
	// single left branch (highly unlikely), we can't ever go deeper than 31
	// levels.
	maxRecursion = 31
)

// Iterate callback. Gets rowid and (possibly truncated) payload. Return true when done
type iterCB func(rowid int64, pl cellPayload) (bool, error)
type tableBtree interface {
	// Iter goes over every record
	Iter(int, *Database, iterCB) (bool, error)
	// Scan starting from a rowid
	IterMin(int, *Database, int64, iterCB) (bool, error)
	// Count counts the number of records. For debugging.
	Count(*Database) (int, error)
}

// indexIterCB gets the Record. It returns true when the iter should be
// stopped.
type indexIterCB func(row Record) (bool, error)

type indexBtree interface {
	// Iter goes over every record
	Iter(int, *Database, indexIterCB) (bool, error)
	// Scan starting from a key
	IterMin(int, *Database, Key, indexIterCB) (bool, error)
	// Count counts the number of records. For debugging.
	Count(*Database) (int, error)
}

type tableLeafCell struct {
	left    int64 // rowID
	payload cellPayload
}
type tableLeaf struct {
	cells []tableLeafCell
}

type tableInteriorCell struct {
	left int
	key  int64
}
type tableInterior struct {
	cells     []tableInteriorCell
	rightmost int
}

type indexLeaf struct {
	cells []cellPayload
}

type indexInteriorCell struct {
	left    int // pageID
	payload cellPayload
}
type indexInterior struct {
	cells     []indexInteriorCell
	rightmost int
}

var (
	_ tableBtree = &tableLeaf{}
	_ tableBtree = &tableInterior{}
	_ indexBtree = &indexLeaf{}
	_ indexBtree = &indexInterior{}
)

func newBtree(b []byte, isFileHeader bool, pageSize int) (interface{}, error) {
	hb := b
	if isFileHeader {
		hb = b[headerSize:]
	}
	cells := int(binary.BigEndian.Uint16(hb[3:5]))
	switch typ := int(hb[0]); typ {
	case 0x0d:
		return newLeafTableBtree(cells, hb[8:], b, pageSize)
	case 0x05:
		rightmostPointer := int(binary.BigEndian.Uint32(hb[8:12]))
		return newInteriorTableBtree(cells, hb[12:], b, rightmostPointer)
	case 0x0a:
		return newLeafIndex(cells, b[8:], b, pageSize)
	case 0x02:
		rightmostPointer := int(binary.BigEndian.Uint32(b[8:12]))
		return newInteriorIndex(cells, b[12:], b, rightmostPointer, pageSize)
	default:
		return nil, errors.New("unsupported page type")
	}
}

func newLeafTableBtree(
	count int,
	pointers []byte,
	content []byte,
	pageSize int,
) (*tableLeaf, error) {
	cells, err := parseCellpointers(count, pointers, len(content))
	if err != nil {
		return nil, err
	}
	leafs := make([]tableLeafCell, len(cells))
	for i, start := range cells {
		leafs[i], err = parseTableLeaf(content[start:], pageSize)
		if err != nil {
			return nil, err
		}
	}
	return &tableLeaf{
		cells: leafs,
	}, nil
}

func (l *tableLeaf) Count(*Database) (int, error) {
	return len(l.cells), nil
}

func (l *tableLeaf) Iter(_ int, _ *Database, cb iterCB) (bool, error) {
	for _, c := range l.cells {
		if done, err := cb(c.left, c.payload); done || err != nil {
			return done, err
		}
	}
	return false, nil
}

func (l *tableLeaf) IterMin(_ int, db *Database, rowid int64, cb iterCB) (bool, error) {
	n := sort.Search(len(l.cells), func(n int) bool {
		return l.cells[n].left >= rowid
	})
	for _, c := range l.cells[n:] {
		return cb(c.left, c.payload)
	}
	return false, nil
}

func newInteriorTableBtree(
	count int,
	pointers []byte,
	content []byte,
	rightmost int,
) (*tableInterior, error) {
	cells, err := parseCellpointers(count, pointers, len(content))
	if err != nil {
		return nil, err
	}
	cs := make([]tableInteriorCell, len(cells))
	for i, start := range cells {
		cs[i], err = parseTableInterior(content[start:])
		if err != nil {
			return nil, err
		}
	}
	return &tableInterior{
		cells:     cs,
		rightmost: rightmost,
	}, nil
}

type interiorIterCB func(page int) (bool, error)

func (l *tableInterior) cellIter(db *Database, cb interiorIterCB) (bool, error) {
	for _, c := range l.cells {
		if done, err := cb(c.left); done || err != nil {
			return done, err
		}
	}
	return cb(l.rightmost)
}

func (l *tableInterior) cellIterMin(db *Database, rowid int64, cb interiorIterCB) (bool, error) {
	n := sort.Search(len(l.cells), func(n int) bool {
		return l.cells[n].key >= rowid
	})
	for _, c := range l.cells[n:] {
		if done, err := cb(c.left); done || err != nil {
			return done, err
		}
	}
	return cb(l.rightmost)
}

func (l *tableInterior) Count(db *Database) (int, error) {
	total := 0
	l.cellIter(db, func(p int) (bool, error) {
		page, err := db.openTable(p)
		if err != nil {
			return false, err
		}
		n, err := page.Count(db)
		if err != nil {
			return false, err
		}
		total += n
		return false, nil
	})
	return total, nil
}

func (l *tableInterior) Iter(r int, db *Database, cb iterCB) (bool, error) {
	if r == 0 {
		return false, ErrRecursion
	}
	return l.cellIter(db, func(p int) (bool, error) {
		page, err := db.openTable(p)
		if err != nil {
			return false, err
		}
		if done, err := page.Iter(r-1, db, cb); done || err != nil {
			return done, err
		}
		return false, nil
	})
}

func (l *tableInterior) IterMin(r int, db *Database, rowid int64, cb iterCB) (bool, error) {
	if r == 0 {
		return false, ErrRecursion
	}
	return l.cellIterMin(db, rowid, func(pageID int) (bool, error) {
		page, err := db.openTable(pageID)
		if err != nil {
			return false, err
		}
		return page.IterMin(r-1, db, rowid, cb)
	})
}

func newLeafIndex(
	count int,
	pointers []byte,
	content []byte,
	pageSize int,
) (*indexLeaf, error) {
	cells, err := parseCellpointers(count, pointers, len(content))
	if err != nil {
		return nil, err
	}
	cs := make([]cellPayload, len(cells))
	for i, start := range cells {
		cs[i], err = parseIndexLeaf(content[start:], pageSize)
		if err != nil {
			return nil, err
		}
	}
	return &indexLeaf{
		cells: cs,
	}, nil
}

func (l *indexLeaf) Iter(_ int, db *Database, cb indexIterCB) (bool, error) {
	for _, pl := range l.cells {
		full, err := addOverflow(db, pl)
		if err != nil {
			return false, err
		}
		rec, err := parseRecord(full)
		if err != nil {
			return false, err
		}
		if done, err := cb(rec); done || err != nil {
			return done, err
		}
	}
	return false, nil
}

func (l *indexLeaf) IterMin(_ int, db *Database, key Key, cb indexIterCB) (bool, error) {
	var searchErr error
	n := sort.Search(len(l.cells), func(n int) bool {
		r, err := indexBinSearch(db, l.cells[n], key)
		if err != nil {
			searchErr = err
		}
		return r
	})
	if searchErr != nil {
		return false, searchErr
	}

	for _, pl := range l.cells[n:] {
		full, err := addOverflow(db, pl)
		if err != nil {
			return false, err
		}
		rec, err := parseRecord(full)
		if err != nil {
			return false, err
		}

		if done, err := cb(rec); done || err != nil {
			return done, err
		}
	}
	return false, nil
}

func (l *indexLeaf) Count(*Database) (int, error) {
	return len(l.cells), nil
}

func newInteriorIndex(
	count int,
	pointers []byte,
	content []byte,
	rightmost int,
	pageSize int,
) (*indexInterior, error) {
	cells, err := parseCellpointers(count, pointers, len(content))
	if err != nil {
		return nil, err
	}
	cs := make([]indexInteriorCell, len(cells))
	for i, start := range cells {
		cs[i], err = parseIndexInterior(content[start:], pageSize)
		if err != nil {
			return nil, err
		}
	}
	return &indexInterior{
		cells:     cs,
		rightmost: rightmost,
	}, nil
}

func (l *indexInterior) Iter(r int, db *Database, cb indexIterCB) (bool, error) {
	if r == 0 {
		return false, ErrRecursion
	}
	for _, c := range l.cells {
		page, err := db.openIndex(c.left)
		if err != nil {
			return false, err
		}
		if done, err := page.Iter(r-1, db, cb); done || err != nil {
			return done, err
		}

		// the btree node also has a record
		full, err := addOverflow(db, c.payload)
		if err != nil {
			return false, err
		}
		rec, err := parseRecord(full)
		if err != nil {
			return false, err
		}
		if done, err := cb(rec); done || err != nil {
			return done, err
		}
	}

	page, err := db.openIndex(l.rightmost)
	if err != nil {
		return false, err
	}
	return page.Iter(r-1, db, cb)
}

func (l *indexInterior) IterMin(r int, db *Database, key Key, cb indexIterCB) (bool, error) {
	if r == 0 {
		return false, ErrRecursion
	}

	// binary search the page containing the range of the key
	var searchErr error
	n := sort.Search(len(l.cells), func(n int) bool {
		r, err := indexBinSearch(db, l.cells[n].payload, key)
		if err != nil {
			searchErr = err
		}
		return r
	})
	if searchErr != nil {
		return false, searchErr
	}

	useIter := false
	for _, c := range l.cells[n:] {
		page, err := db.openIndex(c.left)
		if err != nil {
			return false, err
		}
		if useIter {
			if done, err := page.Iter(r-1, db, cb); done || err != nil {
				return done, err
			}
		} else {
			if done, err := page.IterMin(r-1, db, key, cb); done || err != nil {
				return done, err
			}
		}
		useIter = true // from now on we can simply scan

		// the node has a record, too
		full, err := addOverflow(db, c.payload)
		if err != nil {
			return false, err
		}
		rec, err := parseRecord(full)
		if err != nil {
			return false, err
		}
		if done, err := cb(rec); done || err != nil {
			return done, err
		}
	}
	page, err := db.openIndex(l.rightmost)
	if err != nil {
		return false, err
	}

	if useIter {
		return page.Iter(r-1, db, cb)
	} else {
		return page.IterMin(r-1, db, key, cb)
	}
}

func (l *indexInterior) Count(db *Database) (int, error) {
	total := 0
	for _, c := range l.cells {
		page, err := db.openIndex(c.left)
		if err != nil {
			return 0, err
		}
		n, err := page.Count(db)
		if err != nil {
			return 0, err
		}
		total += n
		total += 1 // the btree node has a record, too
	}

	page, err := db.openIndex(l.rightmost)
	if err != nil {
		return 0, err
	}
	n, err := page.Count(db)
	return total + n, err
}

func calculateCellInPageBytes(l int64, pageSize int, maxInPagePayload int) int {
	// Overflow calculation described in the file format spec. The
	// variable names and magic constants are from the spec exactly.
	u := int64(pageSize)
	p := l
	x := int64(maxInPagePayload)
	m := ((u - 12) * 32 / 255) - 23
	k := m + ((p - m) % (u - 4))

	if p <= x {
		return int(l)
	} else if k <= x {
		return int(k)
	} else {
		return int(m)
	}
}

// shared code for parsing payload from cells
func parsePayload(l int64, c []byte, pageSize int, maxInPagePayload int) (cellPayload, error) {
	overflow := 0
	inPageBytes := calculateCellInPageBytes(l, pageSize, maxInPagePayload)
	if l < 0 {
		return cellPayload{}, ErrCorrupted
	}

	if int64(inPageBytes) == l {
		return cellPayload{l, c, 0}, nil
	}

	if len(c) < inPageBytes+4 {
		return cellPayload{}, ErrCorrupted
	}

	c, overflow = c[:inPageBytes], int(binary.BigEndian.Uint32(c[inPageBytes:inPageBytes+4]))
	if overflow == 0 {
		return cellPayload{}, ErrCorrupted
	}
	return cellPayload{l, c, overflow}, nil
}

func parseTableLeaf(c []byte, pageSize int) (tableLeafCell, error) {
	l, n := readVarint(c)
	if n < 0 {
		return tableLeafCell{}, ErrCorrupted
	}
	c = c[n:]
	rowid, n := readVarint(c)
	if n < 0 {
		return tableLeafCell{}, ErrCorrupted
	}

	pl, err := parsePayload(l, c[n:], pageSize, pageSize-35)
	return tableLeafCell{
		left:    rowid,
		payload: pl,
	}, err
}

func parseTableInterior(c []byte) (tableInteriorCell, error) {
	if len(c) < 4 {
		return tableInteriorCell{}, ErrCorrupted
	}
	left := int(binary.BigEndian.Uint32(c[:4]))
	key, n := readVarint(c[4:])
	if n < 0 {
		return tableInteriorCell{}, ErrCorrupted
	}
	return tableInteriorCell{
		left: left,
		key:  key,
	}, nil
}

func parseIndexLeaf(c []byte, pageSize int) (cellPayload, error) {
	l, n := readVarint(c)
	if n < 0 {
		return cellPayload{}, ErrCorrupted
	}
	return parsePayload(l, c[n:], pageSize, ((pageSize-12)*64/255)-23)
}

func parseIndexInterior(c []byte, pageSize int) (indexInteriorCell, error) {
	if len(c) < 4 {
		return indexInteriorCell{}, ErrCorrupted
	}
	left := int(binary.BigEndian.Uint32(c[:4]))
	c = c[4:]
	l, n := readVarint(c)
	if n < 0 {
		return indexInteriorCell{}, ErrCorrupted
	}
	pl, err := parsePayload(l, c[n:], pageSize, ((pageSize-12)*64/255)-23)
	return indexInteriorCell{
		left:    int(left),
		payload: pl,
	}, err
}

// Parse the list of pointers to cells into byte offsets for each cell
// This format is used in all four page types.
// N is the nr of cells, pointers point to the start of the cells, until end of
// the page, maxLen is the length of the page (because cell pointers use page
// offsets).
func parseCellpointers(
	n int,
	pointers []byte,
	maxLen int,
) ([]int, error) {
	if len(pointers) < n*2 {
		return nil, errors.New("invalid cell pointer array")
	}
	cs := make([]int, n)
	// cell pointers go [p1, p2, p3], actual cell content can be in any order.
	for i := range cs {
		start := int(binary.BigEndian.Uint16(pointers[2*i : 2*i+2]))
		if start > maxLen {
			return nil, errors.New("invalid cell pointer")
		}
		cs[i] = start
	}
	return cs, nil
}

func indexBinSearch(db *Database, pl cellPayload, key Key) (bool, error) {
	// It would be possible to not load the record by default but compare with
	// what's available, and to only call addOverflow() when that data is
	// needed.
	full, err := addOverflow(db, pl)
	if err != nil {
		return true, err
	}
	rec, err := parseRecord(full)
	if err != nil {
		return true, err
	}

	return Search(key, rec), nil
}

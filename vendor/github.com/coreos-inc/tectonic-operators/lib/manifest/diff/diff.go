// Package diff implements 2-way and 3-way diff and merge algorithms.
//
// TODO(diegs): expose more diff methods and types as part of the public API.
package diff

import "fmt"

// Eq denotes a type that can be compared for equality.
type Eq interface {
	// Eq is used to compare two values for equality. The argument is always guaranteed to be the same
	// type as the receiver.
	Eq(Eq) bool
}

// Diff3Merge performs a 3-way merge of a, b, and c, where a is the parent. It returns the merged
// results if there are no conflicts, or error if a conflict was encountered.
func Diff3Merge(a, b, c []Eq) ([]Eq, error) {
	hunks, err := diff3(a, b, c)
	if err != nil {
		return nil, err
	}
	return merge3(hunks)
}

// hunk is a sum type for the types of contiguous sequences of changes in a 3-way merge.
type hunk interface {
	kind() int
	append(left, right chunk) hunk
}

// The 4 kinds of diff hunks.
const (
	// this hunk is unchanged from the parent.
	hunkUnchanged = iota
	// this hunk includes changes from the left child.
	hunkLeftChange
	// this hunk includes changes from the right child.
	hunkRightChange
	// this hunk includes conflicting changes from both children.
	hunkConflict
)

type unchanged struct {
	chunks []chunk
}

func (unchanged) kind() int {
	return hunkUnchanged
}

func (u unchanged) append(c, _ chunk) hunk {
	u.chunks = append(u.chunks, c)
	return u
}

func (u unchanged) String() string {
	return fmt.Sprintf("unchanged: %v", u.chunks)
}

type leftChange struct {
	chunks []chunk
}

func (leftChange) kind() int {
	return hunkLeftChange
}

func (l leftChange) append(c, _ chunk) hunk {
	l.chunks = append(l.chunks, c)
	return l
}

func (l leftChange) String() string {
	return fmt.Sprintf("leftChange: %v", l.chunks)
}

type rightChange struct {
	chunks []chunk
}

func (rightChange) kind() int {
	return hunkRightChange
}

func (r rightChange) append(_, c chunk) hunk {
	r.chunks = append(r.chunks, c)
	return r
}

func (r rightChange) String() string {
	return fmt.Sprintf("rightChange: %v", r.chunks)
}

type conflict struct {
	leftChange  []chunk
	rightChange []chunk
}

func (conflict) kind() int {
	return hunkConflict
}

func (c conflict) append(left, right chunk) hunk {
	c.leftChange = append(c.leftChange, left)
	c.rightChange = append(c.rightChange, right)
	return c
}

func (c conflict) String() string {
	return fmt.Sprintf("conflict: {leftChange=%v, rightChange=%v}", c.leftChange, c.rightChange)
}

// hunkBuilder is a helper to build up hunks from a sequence of chunks.
type hunkBuilder struct {
	hunks []hunk
}

func (h *hunkBuilder) appendLeftChange(c chunk) {
	if len(h.hunks) == 0 || h.hunks[len(h.hunks)-1].kind() != hunkLeftChange {
		h.hunks = append(h.hunks, leftChange{chunks: []chunk{c}})
	} else {
		h.hunks[len(h.hunks)-1] = h.hunks[len(h.hunks)-1].append(c, c)
	}
}

func (h *hunkBuilder) appendRightChange(c chunk) {
	if len(h.hunks) == 0 || h.hunks[len(h.hunks)-1].kind() != hunkRightChange {
		h.hunks = append(h.hunks, rightChange{chunks: []chunk{c}})
	} else {
		h.hunks[len(h.hunks)-1] = h.hunks[len(h.hunks)-1].append(c, c)
	}
}

func (h *hunkBuilder) appendUnchanged(left, right chunk) {
	if len(h.hunks) == 0 || h.hunks[len(h.hunks)-1].kind() != hunkUnchanged {
		h.hunks = append(h.hunks, unchanged{chunks: []chunk{left}})
	} else {
		h.hunks[len(h.hunks)-1] = h.hunks[len(h.hunks)-1].append(left, right)
	}
}

func (h *hunkBuilder) appendConflict(left, right chunk) {
	if len(h.hunks) == 0 || h.hunks[len(h.hunks)-1].kind() != hunkConflict {
		h.hunks = append(h.hunks, conflict{leftChange: []chunk{left}, rightChange: []chunk{right}})
	} else {
		h.hunks[len(h.hunks)-1] = h.hunks[len(h.hunks)-1].append(left, right)
	}
}

// diff3 implements a 3-way diff using the same algorithm as the standard `diff3` program.
func diff3(a, b, c []Eq) ([]hunk, error) {
	ab := diff2(a, b)
	ac := diff2(a, c)
	out := &hunkBuilder{}
	for i, j := 0, 0; i < len(ab) || j < len(ac); {
		switch {
		case i >= len(ab):
			out.appendRightChange(ac[j])
			j++
		case j >= len(ac):
			out.appendLeftChange(ab[i])
			i++
		case ab[i].kind() == ac[j].kind() && ab[i].val().Eq(ac[j].val()):
			out.appendUnchanged(ab[i], ac[j])
			i++
			j++
		case ab[i].kind() != chunkKeep && ac[j].kind() == chunkKeep:
			out.appendLeftChange(ab[i])
			if ab[i].kind() == chunkDel {
				j++
			}
			i++
		case ab[i].kind() == chunkKeep && ac[j].kind() != chunkKeep:
			out.appendRightChange(ac[j])
			if ac[j].kind() == chunkDel {
				i++
			}
			j++
		default:
			out.appendConflict(ab[i], ac[j])
			i++
			j++
		}
	}
	return out.hunks, nil
}

// merge3 takes a series of hunks and performs a 3-merge. It returns the merged sequence on success,
// or error if it encountered a conflict.
func merge3(hunks []hunk) ([]Eq, error) {
	var merged []Eq
	for i, h := range hunks {
		switch t := h.(type) {
		case leftChange:
			for _, c := range t.chunks {
				if c.kind() != chunkDel {
					merged = append(merged, c.val())
				}
			}
		case rightChange:
			for _, c := range t.chunks {
				if c.kind() != chunkDel {
					merged = append(merged, c.val())
				}
			}
		case unchanged:
			for _, c := range t.chunks {
				if c.kind() != chunkDel {
					merged = append(merged, c.val())
				}
			}
		case conflict:
			return nil, fmt.Errorf("encountered conflict at hunk %d (%v), cannot merge", i, h)
		}
	}
	return merged, nil
}

// chunk is a sum type for the types of changes in diff.
type chunk interface {
	kind() int
	val() Eq
}

// The 3 kinds of diff chunks.
const (
	// the chunk exists in both old and new.
	chunkKeep = iota
	// the chunk was added in new.
	chunkAdd
	// the chunk was deleted in new.
	chunkDel
)

type keep struct {
	v Eq
}

func (keep) kind() int {
	return chunkKeep
}

func (k keep) val() Eq {
	return k.v
}

func (k keep) String() string {
	return fmt.Sprintf("keep: %v", k.v)
}

type add struct {
	v Eq
}

func (add) kind() int {
	return chunkAdd
}

func (a add) val() Eq {
	return a.v
}

func (a add) String() string {
	return fmt.Sprintf("add: %v", a.v)
}

type del struct {
	v Eq
}

func (del) kind() int {
	return chunkDel
}

func (d del) val() Eq {
	return d.v
}

func (d del) String() string {
	return fmt.Sprintf("del: %v", d.v)
}

// diff2 implements the Myers diff algorithm. See http://www.xmailserver.org/diff2.pdf and
// elsewhere. It returns a series of chunks that represent the diff.
func diff2(a, b []Eq) []chunk {
	n, m := len(a), len(b)
	max := n + m
	if max == 0 {
		return nil
	}

	v := make([]int, 2*max+1)
	v[1] = 0
	var trace [][]int

	// Compute the diff using the Myers diff algorithm.
myers:
	for d := 0; d <= max; d++ {
		vCopy := make([]int, len(v))
		copy(vCopy, v)
		trace = append(trace, vCopy)

		var x, y int
		for k := -d; k <= d; k += 2 {
			idx := k + len(v)/2 // Negative index hack. The actual indices used don't matter.

			// Determine whether this snake should go right or down.
			if k == -d || (k != d && v[idx-1] < v[idx+1]) {
				x = v[idx+1]
			} else {
				x = v[idx-1] + 1
			}

			// Add diagonals to snake if possible.
			y = x - k
			for x < n && y < m && a[x].Eq(b[y]) {
				x++
				y++
			}

			// Record new position.
			v[idx] = x

			// Stopping condition (x2).
			if x >= n && y >= m {
				break myers
			}
		}
	}

	// Follow the trace to build the (backwards) diff.
	x, y := n, m
	var diff []int
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		idx := k + len(v)/2
		var prevK int
		if k == -d || (k != d && v[idx-1] < v[idx+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := v[prevK+len(v)/2]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			diff = append(diff, chunkKeep)
			x--
			y--
		}
		if d > 0 {
			if x == prevX {
				diff = append(diff, chunkAdd)
			} else {
				diff = append(diff, chunkDel)
			}
		}
		x, y = prevX, prevY
	}

	// Build chunks.
	i, j := 0, 0
	chunks := make([]chunk, len(diff))
	for c := range chunks {
		current := diff[len(diff)-c-1]
		switch current {
		case chunkKeep:
			chunks[c] = keep{a[i]}
			i++
			j++
		case chunkAdd:
			chunks[c] = add{b[j]}
			j++
		case chunkDel:
			chunks[c] = del{a[i]}
			i++
		}
	}
	return chunks
}

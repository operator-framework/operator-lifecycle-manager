package xo

import "github.com/irifrance/gini/z"

type phases z.Var

func (p phases) init(s *S) phases {
	if s.Vars.Max == z.Var(p) {
		return p
	}
	M := s.Vars.Max
	N := 2*M + 2
	counts := make([]uint64, N)
	L := uint64(16)
	D := s.Cdb.CDat.D
	for _, p := range s.Cdb.Added {
		hd := Chd(D[p-1])
		sz := uint64(hd.Size())
		if sz >= L {
			continue
		}
		var m z.Lit
		q := p
		for uint32(q-p) < uint32(sz) {
			m = D[q]
			if m == z.LitNull {
				break
			}
			counts[m] += 1 << (L - sz)
			q++
		}
	}
	cache := s.Guess.cache
	for i := z.Var(1); i <= M; i++ {
		m, n := i.Pos(), i.Neg()
		if counts[m] > counts[n] {
			cache[i] = 1
		} else {
			cache[i] = -1
		}
	}
	return phases(M)
}

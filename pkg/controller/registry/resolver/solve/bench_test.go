package solve

import (
	"context"
	"math/rand"
	"strconv"
	"testing"
)

var BenchmarkInput = func() []Installable {
	const (
		length      = 256
		seed        = 9
		pMandatory  = .1
		pDependency = .15
		nDependency = 6
		pConflict   = .05
		nConflict   = 3
	)

	id := func(i int) Identifier {
		return Identifier(strconv.Itoa(i))
	}

	installable := func(i int) TestInstallable {
		var c []Constraint
		if rand.Float64() < pMandatory {
			c = append(c, Mandatory())
		}
		if rand.Float64() < pDependency {
			n := rand.Intn(nDependency-1) + 1
			var d []Identifier
			for x := 0; x < n; x++ {
				y := i
				for y == i {
					y = rand.Intn(length)
				}
				d = append(d, id(y))
			}
			c = append(c, Dependency(d...))
		}
		if rand.Float64() < pConflict {
			n := rand.Intn(nConflict-1) + 1
			for x := 0; x < n; x++ {
				y := i
				for y == i {
					y = rand.Intn(length)
				}
				c = append(c, Conflict(id(y)))
			}
		}
		return TestInstallable{
			identifier:  id(i),
			constraints: c,
		}
	}

	rand.Seed(seed)
	result := make([]Installable, length)
	for i := range result {
		result[i] = installable(i)
	}
	return result
}()

func BenchmarkSolve(b *testing.B) {
	for i := 0; i < b.N; i++ {
		s, err := New(WithInput(BenchmarkInput))
		if err != nil {
			b.Fatalf("failed to initialize solver: %s", err)
		}
		s.Solve(context.Background())
	}
}

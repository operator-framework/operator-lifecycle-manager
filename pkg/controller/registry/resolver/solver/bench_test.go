package solver

import (
	"context"
	"math/rand"
	"strconv"
	"testing"
)

var BenchmarkInput = func() []Variable {
	const (
		length      = 256
		seed        = 9
		pMandatory  = .1
		pDependency = .15
		nDependency = 6
		pConflict   = .05
		nConflict   = 3
	)

	rnd := rand.New(rand.NewSource(seed))

	id := func(i int) Identifier {
		return Identifier(strconv.Itoa(i))
	}

	variable := func(i int) TestVariable {
		var c []Constraint
		if rnd.Float64() < pMandatory {
			c = append(c, Mandatory())
		}
		if rnd.Float64() < pDependency {
			n := rnd.Intn(nDependency-1) + 1
			var d []Identifier
			for x := 0; x < n; x++ {
				y := i
				for y == i {
					y = rnd.Intn(length)
				}
				d = append(d, id(y))
			}
			c = append(c, Dependency(d...))
		}
		if rnd.Float64() < pConflict {
			n := rnd.Intn(nConflict-1) + 1
			for x := 0; x < n; x++ {
				y := i
				for y == i {
					y = rnd.Intn(length)
				}
				c = append(c, Conflict(id(y)))
			}
		}
		return TestVariable{
			identifier:  id(i),
			constraints: c,
		}
	}

	result := make([]Variable, length)
	for i := range result {
		result[i] = variable(i)
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

func BenchmarkNewInput(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := New(WithInput(BenchmarkInput))
		if err != nil {
			b.Fatalf("failed to initialize solver: %s", err)
		}
	}
}

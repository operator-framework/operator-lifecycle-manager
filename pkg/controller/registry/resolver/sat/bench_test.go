package sat

import (
	"context"
	"math/rand"
	"strconv"
	"testing"

	"github.com/irifrance/gini"
)

func generateInput() []Installable {
	const (
		length      = 256
		seed        = 9
		pMandatory  = .1
		pDependency = .15
		nDependency = 6
		pConflict   = .05
		nConflict   = 3
		pWeight     = 0
		nWeight     = 4
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
		if rand.Float64() < pWeight {
			n := rand.Intn(nWeight-1) + 1
			c = append(c, Weight(n))
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
}

func BenchmarkCompileDict(b *testing.B) {
	input := generateInput()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		compileDict(input)
	}
}

func BenchmarkDictSolve(b *testing.B) {
	d := compileDict(generateInput())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g := gini.New()
		d.Solve(context.Background(), g)
	}
}

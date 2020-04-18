package sat

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

type TestInstallable struct {
	identifier  Identifier
	constraints []Constraint
}

func (i TestInstallable) Identifier() Identifier {
	return i.identifier
}

func (i TestInstallable) Constraints() []Constraint {
	return i.constraints
}

func installable(id Identifier, constraints ...Constraint) Installable {
	return TestInstallable{
		identifier:  id,
		constraints: constraints,
	}
}

func TestNotSatisfiableError(t *testing.T) {
	type tc struct {
		Name   string
		Error  NotSatisfiable
		String string
	}

	for _, tt := range []tc{
		{
			Name:   "nil",
			String: "constraints not satisfiable",
		},
		{
			Name:   "empty",
			String: "constraints not satisfiable",
			Error:  NotSatisfiable{},
		},
		{
			Name: "single failure",
			Error: NotSatisfiable{
				AppliedConstraint{
					Installable: installable("a", Mandatory()),
					Constraint:  Mandatory(),
				},
			},
			String: fmt.Sprintf("constraints not satisfiable: %s",
				Mandatory().String("a")),
		},
		{
			Name: "multiple failures",
			Error: NotSatisfiable{
				AppliedConstraint{
					Installable: installable("a", Mandatory()),
					Constraint:  Mandatory(),
				},
				AppliedConstraint{
					Installable: installable("b", Prohibited()),
					Constraint:  Prohibited(),
				},
			},
			String: fmt.Sprintf("constraints not satisfiable: %s, %s",
				Mandatory().String("a"), Prohibited().String("b")),
		},
	} {
		t.Run(tt.Name, func(t *testing.T) {
			assert.Equal(t, tt.String, tt.Error.Error())
		})
	}
}

func TestSolve(t *testing.T) {
	type tc struct {
		Name         string
		Installables []Installable
		Installed    []Installable
		Error        error
	}

	for _, tt := range []tc{
		{
			Name: "no installables",
		},
		{
			Name:         "unnecessary installable is not installed",
			Installables: []Installable{installable("a")},
		},
		{
			Name:         "single mandatory installable is installed",
			Installables: []Installable{installable("a", Mandatory())},
			Installed:    []Installable{installable("a", Mandatory())},
		},
		{
			Name:         "both mandatory and prohibited produce error",
			Installables: []Installable{installable("a", Mandatory(), Prohibited())},
			Error: NotSatisfiable{
				{
					Installable: installable("a", Mandatory(), Prohibited()),
					Constraint:  Mandatory(),
				},
				{
					Installable: installable("a", Mandatory(), Prohibited()),
					Constraint:  Prohibited(),
				},
			},
		},
		{
			Name: "dependency is installed",
			Installables: []Installable{
				installable("a"),
				installable("b", Mandatory(), Dependency("a")),
			},
			Installed: []Installable{
				installable("a"),
				installable("b", Mandatory(), Dependency("a")),
			},
		},
		{
			Name: "transitive dependency is installed",
			Installables: []Installable{
				installable("a"),
				installable("b", Dependency("a")),
				installable("c", Mandatory(), Dependency("b")),
			},
			Installed: []Installable{
				installable("a"),
				installable("b", Dependency("a")),
				installable("c", Mandatory(), Dependency("b")),
			},
		},
		{
			Name: "both dependencies are installed",
			Installables: []Installable{
				installable("a"),
				installable("b"),
				installable("c", Mandatory(), Dependency("a"), Dependency("b")),
			},
			Installed: []Installable{
				installable("a"),
				installable("b"),
				installable("c", Mandatory(), Dependency("a"), Dependency("b")),
			},
		},
		{
			Name: "solution with lowest weight is selected",
			Installables: []Installable{
				installable("a"),
				installable("b", Weight(1), Conflict("a")),
				installable("c", Mandatory(), Dependency("a", "b")),
			},
			Installed: []Installable{
				installable("a"),
				installable("c", Mandatory(), Dependency("a", "b")),
			},
		},
		{
			Name: "solution with lowest weight is selected (reverse)",
			Installables: []Installable{
				installable("a", Weight(1)),
				installable("b", Conflict("a")),
				installable("c", Mandatory(), Dependency("a", "b")),
			},
			Installed: []Installable{
				installable("b", Conflict("a")),
				installable("c", Mandatory(), Dependency("a", "b")),
			},
		},
		{
			Name: "two mandatory but conflicting packages",
			Installables: []Installable{
				installable("a", Mandatory()),
				installable("b", Mandatory(), Conflict("a")),
			},
			Error: NotSatisfiable{
				{
					Installable: installable("a", Mandatory()),
					Constraint:  Mandatory(),
				},
				{
					Installable: installable("b", Mandatory(), Conflict("a")),
					Constraint:  Mandatory(),
				},
				{
					Installable: installable("b", Mandatory(), Conflict("a")),
					Constraint:  Conflict("a"),
				},
			},
		},
	} {
		t.Run(tt.Name, func(t *testing.T) {
			assert := assert.New(t)

			installed, err := Solve(tt.Installables)

			if installed != nil {
				sort.SliceStable(installed, func(i, j int) bool {
					return installed[i].Identifier() < installed[j].Identifier()
				})
			}

			// Failed constraints are sorted in lexically
			// increasing order of the identifier of the
			// constraint's installable, with ties broken
			// in favor of the constraint that appears
			// earliest in the installable's list of
			// constraints.
			if ns, ok := err.(NotSatisfiable); ok {
				sort.SliceStable(ns, func(i, j int) bool {
					if ns[i].Installable.Identifier() != ns[j].Installable.Identifier() {
						return ns[i].Installable.Identifier() < ns[j].Installable.Identifier()
					}
					var x, y int
					for ii, c := range ns[i].Installable.Constraints() {
						if reflect.DeepEqual(c, ns[i].Constraint) {
							x = ii
							break
						}
					}
					for ij, c := range ns[j].Installable.Constraints() {
						if reflect.DeepEqual(c, ns[j].Constraint) {
							y = ij
							break
						}
					}
					return x < y
				})
			}

			assert.Equal(tt.Installed, installed)
			assert.Equal(tt.Error, err)
		})
	}
}

func TestSolveWithWeight(t *testing.T) {
	assert := assert.New(t)

	id := func(i int) Identifier {
		return Identifier(strconv.Itoa(i + 1))
	}

	installables := make([]Installable, 256)
	c := make(dependency, len(installables)-1)
	for i := 0; i < len(installables)-1; i++ {
		installables[i] = TestInstallable{
			identifier:  id(i),
			constraints: []Constraint{Weight(1)},
		}
		c[i] = id(i)
	}
	installables[len(installables)-2] = TestInstallable{
		identifier: id(len(installables) - 2),
	}
	rand.Seed(42)
	rand.Shuffle(len(installables)-1, func(i, j int) {
		installables[i], installables[j] = installables[j], installables[i]
		c[i], c[j] = c[j], c[i]
	})
	installables[len(installables)-1] = TestInstallable{
		identifier:  id(len(installables) - 1),
		constraints: []Constraint{Mandatory(), c},
	}

	result, err := Solve(installables)

	sort.Slice(result, func(i, j int) bool {
		return result[i].Identifier() < result[j].Identifier()
	})

	assert.NoError(err)
	assert.Equal([]Installable{
		TestInstallable{
			identifier: id(len(installables) - 2),
		},
		TestInstallable{
			identifier:  id(len(installables) - 1),
			constraints: []Constraint{Mandatory(), c},
		},
	}, result)
}

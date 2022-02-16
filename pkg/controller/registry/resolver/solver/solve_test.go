package solver

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
)

type TestVariable struct {
	identifier  Identifier
	constraints []Constraint
}

func (i TestVariable) Identifier() Identifier {
	return i.identifier
}

func (i TestVariable) Constraints() []Constraint {
	return i.constraints
}

func (i TestVariable) GoString() string {
	return fmt.Sprintf("%q", i.Identifier())
}

func variable(id Identifier, constraints ...Constraint) Variable {
	return TestVariable{
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
					Variable:   variable("a", Mandatory()),
					Constraint: Mandatory(),
				},
			},
			String: fmt.Sprintf("constraints not satisfiable: %s",
				Mandatory().String("a")),
		},
		{
			Name: "multiple failures",
			Error: NotSatisfiable{
				AppliedConstraint{
					Variable:   variable("a", Mandatory()),
					Constraint: Mandatory(),
				},
				AppliedConstraint{
					Variable:   variable("b", Prohibited()),
					Constraint: Prohibited(),
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
		Name      string
		Variables []Variable
		Installed []Identifier
		Error     error
	}

	for _, tt := range []tc{
		{
			Name: "no variables",
		},
		{
			Name:      "unnecessary variable is not installed",
			Variables: []Variable{variable("a")},
		},
		{
			Name:      "single mandatory variable is installed",
			Variables: []Variable{variable("a", Mandatory())},
			Installed: []Identifier{"a"},
		},
		{
			Name:      "both mandatory and prohibited produce error",
			Variables: []Variable{variable("a", Mandatory(), Prohibited())},
			Error: NotSatisfiable{
				{
					Variable:   variable("a", Mandatory(), Prohibited()),
					Constraint: Mandatory(),
				},
				{
					Variable:   variable("a", Mandatory(), Prohibited()),
					Constraint: Prohibited(),
				},
			},
		},
		{
			Name: "dependency is installed",
			Variables: []Variable{
				variable("a"),
				variable("b", Mandatory(), Dependency("a")),
			},
			Installed: []Identifier{"a", "b"},
		},
		{
			Name: "transitive dependency is installed",
			Variables: []Variable{
				variable("a"),
				variable("b", Dependency("a")),
				variable("c", Mandatory(), Dependency("b")),
			},
			Installed: []Identifier{"a", "b", "c"},
		},
		{
			Name: "both dependencies are installed",
			Variables: []Variable{
				variable("a"),
				variable("b"),
				variable("c", Mandatory(), Dependency("a"), Dependency("b")),
			},
			Installed: []Identifier{"a", "b", "c"},
		},
		{
			Name: "solution with first dependency is selected",
			Variables: []Variable{
				variable("a"),
				variable("b", Conflict("a")),
				variable("c", Mandatory(), Dependency("a", "b")),
			},
			Installed: []Identifier{"a", "c"},
		},
		{
			Name: "solution with only first dependency is selected",
			Variables: []Variable{
				variable("a"),
				variable("b"),
				variable("c", Mandatory(), Dependency("a", "b")),
			},
			Installed: []Identifier{"a", "c"},
		},
		{
			Name: "solution with first dependency is selected (reverse)",
			Variables: []Variable{
				variable("a"),
				variable("b", Conflict("a")),
				variable("c", Mandatory(), Dependency("b", "a")),
			},
			Installed: []Identifier{"b", "c"},
		},
		{
			Name: "two mandatory but conflicting packages",
			Variables: []Variable{
				variable("a", Mandatory()),
				variable("b", Mandatory(), Conflict("a")),
			},
			Error: NotSatisfiable{
				{
					Variable:   variable("a", Mandatory()),
					Constraint: Mandatory(),
				},
				{
					Variable:   variable("b", Mandatory(), Conflict("a")),
					Constraint: Mandatory(),
				},
				{
					Variable:   variable("b", Mandatory(), Conflict("a")),
					Constraint: Conflict("a"),
				},
			},
		},
		{
			Name: "irrelevant dependencies don't influence search order",
			Variables: []Variable{
				variable("a", Dependency("x", "y")),
				variable("b", Mandatory(), Dependency("y", "x")),
				variable("x"),
				variable("y"),
			},
			Installed: []Identifier{"b", "y"},
		},
		{
			Name: "cardinality constraint prevents resolution",
			Variables: []Variable{
				variable("a", Mandatory(), Dependency("x", "y"), AtMost(1, "x", "y")),
				variable("x", Mandatory()),
				variable("y", Mandatory()),
			},
			Error: NotSatisfiable{
				{
					Variable:   variable("a", Mandatory(), Dependency("x", "y"), AtMost(1, "x", "y")),
					Constraint: AtMost(1, "x", "y"),
				},
				{
					Variable:   variable("x", Mandatory()),
					Constraint: Mandatory(),
				},
				{
					Variable:   variable("y", Mandatory()),
					Constraint: Mandatory(),
				},
			},
		},
		{
			Name: "cardinality constraint forces alternative",
			Variables: []Variable{
				variable("a", Mandatory(), Dependency("x", "y"), AtMost(1, "x", "y")),
				variable("b", Mandatory(), Dependency("y")),
				variable("x"),
				variable("y"),
			},
			Installed: []Identifier{"a", "b", "y"},
		},
		{
			Name: "two dependencies satisfied by one variable",
			Variables: []Variable{
				variable("a", Mandatory(), Dependency("y")),
				variable("b", Mandatory(), Dependency("x", "y")),
				variable("x"),
				variable("y"),
			},
			Installed: []Identifier{"a", "b", "y"},
		},
		{
			Name: "foo two dependencies satisfied by one variable",
			Variables: []Variable{
				variable("a", Mandatory(), Dependency("y", "z", "m")),
				variable("b", Mandatory(), Dependency("x", "y")),
				variable("x"),
				variable("y"),
				variable("z"),
				variable("m"),
			},
			Installed: []Identifier{"a", "b", "y"},
		},
		{
			Name: "result size larger than minimum due to preference",
			Variables: []Variable{
				variable("a", Mandatory(), Dependency("x", "y")),
				variable("b", Mandatory(), Dependency("y")),
				variable("x"),
				variable("y"),
			},
			Installed: []Identifier{"a", "b", "x", "y"},
		},
		{
			Name: "only the least preferable choice is acceptable",
			Variables: []Variable{
				variable("a", Mandatory(), Dependency("a1", "a2")),
				variable("a1", Conflict("c1"), Conflict("c2")),
				variable("a2", Conflict("c1")),
				variable("b", Mandatory(), Dependency("b1", "b2")),
				variable("b1", Conflict("c1"), Conflict("c2")),
				variable("b2", Conflict("c1")),
				variable("c", Mandatory(), Dependency("c1", "c2")),
				variable("c1"),
				variable("c2"),
			},
			Installed: []Identifier{"a", "a2", "b", "b2", "c", "c2"},
		},
		{
			Name: "preferences respected with multiple dependencies per variable",
			Variables: []Variable{
				variable("a", Mandatory(), Dependency("x1", "x2"), Dependency("y1", "y2")),
				variable("x1"),
				variable("x2"),
				variable("y1"),
				variable("y2"),
			},
			Installed: []Identifier{"a", "x1", "y1"},
		},
	} {
		t.Run(tt.Name, func(t *testing.T) {
			assert := assert.New(t)

			var traces bytes.Buffer
			s, err := New(WithInput(tt.Variables), WithTracer(LoggingTracer{Writer: &traces}))
			if err != nil {
				t.Fatalf("failed to initialize solver: %s", err)
			}

			installed, err := s.Solve(context.TODO())

			if installed != nil {
				sort.SliceStable(installed, func(i, j int) bool {
					return installed[i].Identifier() < installed[j].Identifier()
				})
			}

			// Failed constraints are sorted in lexically
			// increasing order of the identifier of the
			// constraint's variable, with ties broken
			// in favor of the constraint that appears
			// earliest in the variable's list of
			// constraints.
			if ns, ok := err.(NotSatisfiable); ok {
				sort.SliceStable(ns, func(i, j int) bool {
					if ns[i].Variable.Identifier() != ns[j].Variable.Identifier() {
						return ns[i].Variable.Identifier() < ns[j].Variable.Identifier()
					}
					var x, y int
					for ii, c := range ns[i].Variable.Constraints() {
						if reflect.DeepEqual(c, ns[i].Constraint) {
							x = ii
							break
						}
					}
					for ij, c := range ns[j].Variable.Constraints() {
						if reflect.DeepEqual(c, ns[j].Constraint) {
							y = ij
							break
						}
					}
					return x < y
				})
			}

			var ids []Identifier
			for _, variable := range installed {
				ids = append(ids, variable.Identifier())
			}
			assert.Equal(tt.Installed, ids)
			assert.Equal(tt.Error, err)

			if t.Failed() {
				t.Logf("\n%s", traces.String())
			}
		})
	}
}

func TestDuplicateIdentifier(t *testing.T) {
	_, err := New(WithInput([]Variable{
		variable("a"),
		variable("a"),
	}))
	assert.Equal(t, DuplicateIdentifier("a"), err)
}

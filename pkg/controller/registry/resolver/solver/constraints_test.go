package solver

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConstraints(t *testing.T) {
	type tc struct {
		Name       string
		Constraint Constraint
		Subject    Identifier
		Expected   constrainer
	}

	for _, tt := range []tc{
		{
			Name:       "mandatory",
			Constraint: Mandatory(),
			Subject:    "a",
			Expected: constrainer{
				pos: []Identifier{"a"},
			},
		},
		{
			Name:       "prohibited",
			Constraint: Prohibited(),
			Subject:    "a",
			Expected: constrainer{
				neg: []Identifier{"a"},
			},
		},
		{
			Name:       "empty dependency",
			Constraint: Dependency(),
			Subject:    "a",
			Expected:   constrainer{},
		},
		{
			Name:       "single dependency",
			Constraint: Dependency("b"),
			Subject:    "a",
			Expected: constrainer{
				pos: []Identifier{"b"},
				neg: []Identifier{"a"},
			},
		},
		{
			Name:       "multiple dependency",
			Constraint: Dependency("x", "y", "z"),
			Subject:    "a",
			Expected: constrainer{
				pos: []Identifier{"x", "y", "z"},
				neg: []Identifier{"a"},
			},
		},
		{
			Name:       "conflict",
			Constraint: Conflict("b"),
			Subject:    "a",
			Expected: constrainer{
				neg: []Identifier{"a", "b"},
			},
		},
	} {
		t.Run(tt.Name, func(t *testing.T) {
			var x constrainer
			tt.Constraint.apply(&x, tt.Subject)

			// Literals in lexically increasing order:
			sort.Slice(x.pos, func(i, j int) bool {
				return x.pos[i] < x.pos[j]
			})
			sort.Slice(x.neg, func(i, j int) bool {
				return x.neg[i] < x.neg[j]
			})

			assert.Equal(t, tt.Expected, x)
		})
	}
}

func TestOrder(t *testing.T) {
	type tc struct {
		Name       string
		Constraint Constraint
		Expected   []Identifier
	}

	for _, tt := range []tc{
		{
			Name:       "mandatory",
			Constraint: Mandatory(),
		},
		{
			Name:       "prohibited",
			Constraint: Prohibited(),
		},
		{
			Name:       "dependency",
			Constraint: Dependency("a", "b", "c"),
			Expected:   []Identifier{"a", "b", "c"},
		},
		{
			Name:       "conflict",
			Constraint: Conflict("a"),
		},
	} {
		t.Run(tt.Name, func(t *testing.T) {
			assert.Equal(t, tt.Expected, tt.Constraint.order())
		})
	}
}

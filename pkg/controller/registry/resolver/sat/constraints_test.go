package sat

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
		Expected   cstate
	}

	for _, tt := range []tc{
		{
			Name:       "mandatory",
			Constraint: Mandatory(),
			Subject:    "a",
			Expected: cstate{
				pos: []Identifier{"a"},
			},
		},
		{
			Name:       "prohibited",
			Constraint: Prohibited(),
			Subject:    "a",
			Expected: cstate{
				neg: []Identifier{"a"},
			},
		},
		{
			Name:       "empty dependency",
			Constraint: Dependency(),
			Subject:    "a",
			Expected:   cstate{},
		},
		{
			Name:       "single dependency",
			Constraint: Dependency("b"),
			Subject:    "a",
			Expected: cstate{
				pos: []Identifier{"b"},
				neg: []Identifier{"a"},
			},
		},
		{
			Name:       "multiple dependency",
			Constraint: Dependency("x", "y", "z"),
			Subject:    "a",
			Expected: cstate{
				pos: []Identifier{"x", "y", "z"},
				neg: []Identifier{"a"},
			},
		},
		{
			Name:       "conflict",
			Constraint: Conflict("b"),
			Subject:    "a",
			Expected: cstate{
				neg: []Identifier{"a", "b"},
			},
		},
		{
			Name:       "negative weight",
			Constraint: Weight(-1),
			Subject:    "a",
			Expected:   cstate{},
		},
		{
			Name:       "weight",
			Constraint: Weight(5),
			Subject:    "a",
			Expected: cstate{
				weight: 5,
			},
		},
	} {
		t.Run(tt.Name, func(t *testing.T) {
			var x cstate
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

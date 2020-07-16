package solver

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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

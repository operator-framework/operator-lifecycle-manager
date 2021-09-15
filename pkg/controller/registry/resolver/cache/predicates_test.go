package cache

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type OperatorPredicateTestFunc func(*Entry) bool

func (opf OperatorPredicateTestFunc) Test(o *Entry) bool {
	return opf(o)
}

func (opf OperatorPredicateTestFunc) String() string {
	return ""
}

func TestCountingPredicate(t *testing.T) {
	for _, tc := range []struct {
		Name        string
		TestResults []bool
		Expected    int
	}{
		{
			Name:        "no increment on failure",
			TestResults: []bool{false},
			Expected:    0,
		},
		{
			Name:        "increment on success",
			TestResults: []bool{true},
			Expected:    1,
		},
		{
			Name:        "multiple increments",
			TestResults: []bool{true, true},
			Expected:    2,
		},
		{
			Name:        "no increment without test",
			TestResults: nil,
			Expected:    0,
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			var (
				n      int
				result bool
			)

			p := CountingPredicate(OperatorPredicateTestFunc(func(*Entry) bool {
				return result
			}), &n)

			for _, result = range tc.TestResults {
				p.Test(nil)
			}

			assert.Equal(t, tc.Expected, n)
		})
	}
}

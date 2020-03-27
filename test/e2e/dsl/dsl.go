package dsl

import (
	"fmt"

	. "github.com/onsi/ginkgo"
)

// IgnoreError acknowledges that an error value is being intentionally
// disregarded.
//
// In general, errors should not be ignored, however, errors may be
// unimportant in certain test scenarios. IgnoreError accepts a
// variable-length argument list, like Expect(), as a convenience for
// functions returning values and an error, e.g. `func DoSomething()
// (string, error)`. IgnoreError will fail the current test if the
// last argument is neither nil nor a non-nil error.
func IgnoreError(vals ...interface{}) {
	if len(vals) == 0 {
		return
	}
	err := vals[len(vals)-1]
	if err == nil {
		return
	}
	if _, ok := err.(error); ok {
		return
	}
	Fail(fmt.Sprintf("the last argument to IgnoreError must be an error, but it was %T", err))
}

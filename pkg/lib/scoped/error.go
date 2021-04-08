package scoped

// operatorGroupError is an error type returned by the service account querier when
// there is an invalid operatorGroup (zero groups, multiple groups, non-existent service account)
type operatorGroupError struct {
	s string
}

func NewOperatorGroupError(s string) error {
	return operatorGroupError{s: s}
}

func (e operatorGroupError) Error() string {
	return e.s
}

func (e operatorGroupError) IsOperatorGroupError() bool {
	return true
}

// IsOperatorGroupError checks if an error is an operator group error
// This lets us classify multiple errors as operatorGroupError without
// defining and checking all the specific error value types
func IsOperatorGroupError(err error) bool {
	type operatorGroupError interface {
		IsOperatorGroupError() bool
	}
	ogErr, ok := err.(operatorGroupError)
	return ok && ogErr.IsOperatorGroupError()
}

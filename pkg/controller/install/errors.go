package install

const (
	StrategyErrReasonComponentMissing   = "ComponentMissing"
	StrategyErrReasonAnnotationsMissing = "AnnotationsMissing"
	StrategyErrReasonWaiting            = "Waiting"
	StrategyErrReasonInvalidStrategy    = "InvalidStrategy"
	StrategyErrReasonTimeout            = "Timeout"
	StrategyErrReasonUnknown            = "Unknown"
	StrategyErrBadPatch                 = "PatchUnsuccessful"
	StrategyErrDeploymentUpdated        = "DeploymentUpdated"
	StrategyErrInsufficientPermissions  = "InsufficentPermissions"
)

// unrecoverableErrors are the set of errors that mean we can't recover an install strategy
var unrecoverableErrors = map[string]struct{}{
	StrategyErrReasonInvalidStrategy:   {},
	StrategyErrReasonTimeout:           {},
	StrategyErrBadPatch:                {},
	StrategyErrInsufficientPermissions: {},
}

// StrategyError is used to represent error types for install strategies
type StrategyError struct {
	Reason  string
	Message string
}

var _ error = StrategyError{}

// Error implements the Error interface.
func (e StrategyError) Error() string {
	return e.Message
}

// IsErrorUnrecoverable reports if a given strategy error is one of the predefined unrecoverable types
func IsErrorUnrecoverable(err error) bool {
	if err == nil {
		return false
	}
	_, ok := unrecoverableErrors[ReasonForError(err)]
	return ok
}

func ReasonForError(err error) string {
	switch t := err.(type) {
	case StrategyError:
		return t.Reason
	case *StrategyError:
		return t.Reason
	}
	return StrategyErrReasonUnknown
}

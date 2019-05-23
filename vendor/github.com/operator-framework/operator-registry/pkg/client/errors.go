package client

import (
	"fmt"

	"google.golang.org/grpc"
)

const (
	HealthErrReasonUnrecoveredTransient HealthErrorReason = "UnrecoveredTransient"
	HealthErrReasonConnection           HealthErrorReason = "ConnectionError"
	HealthErrReasonUnknown              HealthErrorReason = "Unknown"
)

type HealthErrorReason string

// HealthError is used to represent error types for health checks
type HealthError struct {
	ClientState string
	Reason      HealthErrorReason
	Message     string
}

var _ error = HealthError{}

// Error implements the Error interface.
func (e HealthError) Error() string {
	return fmt.Sprintf("%s: %s", e.ClientState, e.Message)
}

// unrecoverableErrors are the set of errors that mean we can't recover the connection
var unrecoverableErrors = map[HealthErrorReason]struct{}{
	HealthErrReasonUnrecoveredTransient: {},
}

func NewHealthError(conn *grpc.ClientConn, reason HealthErrorReason, msg string) HealthError {
	return HealthError{
		ClientState: conn.GetState().String(),
		Reason:      reason,
		Message:     msg,
	}
}

// IsErrorUnrecoverable reports if a given error is one of the predefined unrecoverable types
func IsErrorUnrecoverable(err error) bool {
	if err == nil {
		return false
	}
	_, ok := unrecoverableErrors[reasonForError(err)]
	return ok
}

func reasonForError(err error) HealthErrorReason {
	switch t := err.(type) {
	case HealthError:
		return t.Reason
	case *HealthError:
		return t.Reason
	}
	return HealthErrReasonUnknown
}

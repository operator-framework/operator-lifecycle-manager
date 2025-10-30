package errors

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRetryableError(t *testing.T) {
	baseErr := errors.New("test error")

	retryErr := NewRetryableError(baseErr)
	require.True(t, IsRetryable(retryErr), "NewRetryableError should create a retryable error")
	require.Equal(t, baseErr.Error(), retryErr.Error(), "RetryableError should preserve the underlying error message")

	normalErr := errors.New("normal error")
	require.False(t, IsRetryable(normalErr), "Normal error should not be retryable")
}

func TestFatalError(t *testing.T) {
	baseErr := errors.New("test error")

	fatalErr := NewFatalError(baseErr)
	require.True(t, IsFatal(fatalErr), "NewFatalError should create a fatal error")

	normalErr := errors.New("normal error")
	require.False(t, IsFatal(normalErr), "Normal error should not be fatal")
}

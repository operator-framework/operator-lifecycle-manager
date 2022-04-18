package resolver

import (
	"errors"
	"testing"
	"time"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/stretchr/testify/require"
)

const (
	failure = time.Duration(0)
	success = time.Duration(1)
)

type fakeResolverWithError struct{}
type fakeResolverWithoutError struct{}

func (r *fakeResolverWithError) ResolveSteps(namespace string) ([]*v1alpha1.Step, []v1alpha1.BundleLookup, []*v1alpha1.Subscription, error) {
	return nil, nil, nil, errors.New("Fake error")
}

func (r *fakeResolverWithoutError) ResolveSteps(namespace string) ([]*v1alpha1.Step, []v1alpha1.BundleLookup, []*v1alpha1.Subscription, error) {
	return nil, nil, nil, nil
}

func newFakeResolverWithError() *fakeResolverWithError {
	return &fakeResolverWithError{}
}

func newFakeResolverWithoutError() *fakeResolverWithoutError {
	return &fakeResolverWithoutError{}
}

func TestInstrumentedResolverFailure(t *testing.T) {
	result := []time.Duration{}

	changeToFailure := func(num time.Duration) {
		result = append(result, failure)
	}

	changeToSuccess := func(num time.Duration) {
		result = append(result, success)
	}

	instrumentedResolver := NewInstrumentedResolver(newFakeResolverWithError(), changeToSuccess, changeToFailure)
	instrumentedResolver.ResolveSteps("")
	require.Equal(t, len(result), 1)     // check that only one call was made to a change function
	require.Equal(t, result[0], failure) // check that the call was made to changeToFailure function
}

func TestInstrumentedResolverSuccess(t *testing.T) {
	result := []time.Duration{}

	changeToFailure := func(num time.Duration) {
		result = append(result, failure)
	}

	changeToSuccess := func(num time.Duration) {
		result = append(result, success)
	}

	instrumentedResolver := NewInstrumentedResolver(newFakeResolverWithoutError(), changeToSuccess, changeToFailure)
	instrumentedResolver.ResolveSteps("")
	require.Equal(t, len(result), 1)     // check that only one call was made to a change function
	require.Equal(t, result[0], success) // check that the call was made to changeToSuccess function
}

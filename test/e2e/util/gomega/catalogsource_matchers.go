package gomega

import (
	"fmt"

	"github.com/onsi/gomega/matchers"
	"github.com/onsi/gomega/types"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/util"
	"google.golang.org/grpc/connectivity"
)

func HaveGrpcConnectionWithLastConnectionState(state connectivity.State) types.GomegaMatcher {
	return &CatalogSourceGrpcConnectionLastConnectionStateMatcher{
		EqualMatcher: &matchers.EqualMatcher{
			Expected: state.String(),
		},
	}
}

type CatalogSourceGrpcConnectionLastConnectionStateMatcher struct {
	*matchers.EqualMatcher
}

func (s *CatalogSourceGrpcConnectionLastConnectionStateMatcher) FailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("expected catalog source status.grpcConnectionState.lastObservedState to be '%s'\n%s\n", s.Expected, util.ObjectToPrettyJsonString(actual))
}

func (s *CatalogSourceGrpcConnectionLastConnectionStateMatcher) NegatedFailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("expected catalog  status.grpcConnectionState.lastObservedState to NOT be '%s':\n%s\n ", s.Expected, util.ObjectToPrettyJsonString(actual))
}

func (s *CatalogSourceGrpcConnectionLastConnectionStateMatcher) Match(actual interface{}) (bool, error) {
	switch actual := actual.(type) {
	case *v1alpha1.CatalogSource:
		if actual.Status.GRPCConnectionState == nil {
			return false, fmt.Errorf("catalog source does not have a grpc connection state")
		}
		util.Logf("expecting catalog source last connection state '%s' to be '%s'", actual.Status.GRPCConnectionState.LastObservedState, s.Expected)
		return s.EqualMatcher.Match(actual.Status.GRPCConnectionState.LastObservedState)
	default:
		return false, fmt.Errorf("actual %v is not a subscription", actual)
	}
}

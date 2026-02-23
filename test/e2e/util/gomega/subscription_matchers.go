package gomega

import (
	"fmt"

	"github.com/onsi/gomega/matchers"
	"github.com/onsi/gomega/types"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/util"
)

func HaveSubscriptionState(state v1alpha1.SubscriptionState) types.GomegaMatcher {
	return &SubscriptionStateMatcher{
		EqualMatcher: &matchers.EqualMatcher{
			Expected: state,
		},
	}
}

type SubscriptionStateMatcher struct {
	*matchers.EqualMatcher
}

func (s *SubscriptionStateMatcher) FailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("expected subscription status.subscriptionState to be '%s':\n%s\n", s.Expected, util.ObjectToJsonString(actual))
}

func (s *SubscriptionStateMatcher) NegatedFailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("expected subscription status.subscriptionState to NOT be '%s'\n%s\n", s.Expected, util.ObjectToJsonString(actual))
}

func (s *SubscriptionStateMatcher) Match(actual interface{}) (bool, error) {
	switch actual := actual.(type) {
	case *v1alpha1.Subscription:
		util.Logf("expecting subscription state '%s' to be '%s'", actual.Status.State, s.Expected)
		return s.EqualMatcher.Match(actual.Status.State)
	default:
		return false, fmt.Errorf("actual %v is not a subscription", actual)
	}
}

type ContainSubscriptionConditionOfTypeMatcher struct {
	*matchers.ContainElementMatcher
}

func ContainSubscriptionConditionOfType(conditionType v1alpha1.SubscriptionConditionType) types.GomegaMatcher {
	return &ContainSubscriptionConditionOfTypeMatcher{
		ContainElementMatcher: &matchers.ContainElementMatcher{
			Element: conditionType,
		},
	}
}

func (s *ContainSubscriptionConditionOfTypeMatcher) Match(actual interface{}) (bool, error) {
	switch actual := actual.(type) {
	case *v1alpha1.Subscription:
		var conditionTypes []v1alpha1.SubscriptionConditionType
		for _, condition := range actual.Status.Conditions {
			conditionTypes = append(conditionTypes, condition.Type)
		}
		util.Logf("expecting subscription condition type '%s' to be in %s", s.Element, conditionTypes)
		return s.ContainElementMatcher.Match(conditionTypes)
	default:
		return false, fmt.Errorf("actual %v is not a subscription", actual)
	}
}

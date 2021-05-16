package e2e

import (
	"fmt"

	"github.com/onsi/gomega/format"
	"github.com/onsi/gomega/types"
)

type Metric struct {
	Family string
	Labels map[string][]string
	Value  float64 // Zero unless type is Untypted, Gauge, or Counter!
}

type MetricPredicate struct {
	f    func(m Metric) bool
	name string
}

func (mp MetricPredicate) String() string {
	return mp.name
}

func WithFamily(f string) MetricPredicate {
	return MetricPredicate{
		name: fmt.Sprintf("WithFamily(%s)", f),
		f: func(m Metric) bool {
			return m.Family == f
		},
	}
}

func WithLabel(n, v string) MetricPredicate {
	return MetricPredicate{
		name: fmt.Sprintf("WithLabel(%s=%s)", n, v),
		f: func(m Metric) bool {
			for name, values := range m.Labels {
				for _, value := range values {
					if name == n && value == v {
						return true
					}
				}
			}
			return false
		},
	}
}

func WithName(name string) MetricPredicate {
	return WithLabel("name", name)
}

func WithNamespace(namespace string) MetricPredicate {
	return WithLabel("namespace", namespace)
}

func WithChannel(channel string) MetricPredicate {
	return WithLabel("channel", channel)
}

func WithPackage(pkg string) MetricPredicate {
	return WithLabel("package", pkg)
}

func WithPhase(phase string) MetricPredicate {
	return WithLabel("phase", phase)
}

func WithReason(reason string) MetricPredicate {
	return WithLabel("reason", reason)
}

func WithApproval(approvalStrategy string) MetricPredicate {
	return WithLabel("approval", approvalStrategy)
}

func WithVersion(version string) MetricPredicate {
	return WithLabel("version", version)
}

func WithValue(v float64) MetricPredicate {
	return MetricPredicate{
		name: fmt.Sprintf("WithValue(%g)", v),
		f: func(m Metric) bool {
			return m.Value == v
		},
	}
}

func WithValueGreaterThan(v float64) MetricPredicate {
	return MetricPredicate{
		name: fmt.Sprintf("WithValueGreaterThan(%g)", v),
		f: func(m Metric) bool {
			return m.Value > v
		},
	}
}

type LikeMetricMatcher struct {
	Predicates []MetricPredicate
}

func (matcher *LikeMetricMatcher) Match(actual interface{}) (bool, error) {
	metric, ok := actual.(Metric)
	if !ok {
		return false, fmt.Errorf("LikeMetric matcher expects Metric (got %T)", actual)
	}
	for _, predicate := range matcher.Predicates {
		if !predicate.f(metric) {
			return false, nil
		}
	}
	return true, nil
}

func (matcher *LikeMetricMatcher) FailureMessage(actual interface{}) string {
	return format.Message(actual, "to satisfy", matcher.Predicates)
}

func (matcher *LikeMetricMatcher) NegatedFailureMessage(actual interface{}) string {
	return format.Message(actual, "not to satisfy", matcher.Predicates)
}

func LikeMetric(preds ...MetricPredicate) types.GomegaMatcher {
	return &LikeMetricMatcher{
		Predicates: preds,
	}
}

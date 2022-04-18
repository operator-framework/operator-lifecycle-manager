package resolver

import (
	"fmt"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	v1listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// IsFailForwardEnabled takes a namespaced operatorGroup lister and returns
// True if an operatorGroup exists in the namespace and its upgradeStrategy
// is set to UnsafeFailForward and false otherwise. An error is returned if
// an more than one operatorGroup exists in the namespace.
// No error is returned if no OperatorGroups are found to keep the resolver
// backwards compatible.
func IsFailForwardEnabled(ogLister v1listers.OperatorGroupNamespaceLister) (bool, error) {
	ogs, err := ogLister.List(labels.Everything())
	if err != nil || len(ogs) == 0 {
		return false, nil
	}
	if len(ogs) != 1 {
		return false, fmt.Errorf("found %d operatorGroups, expected 1", len(ogs))
	}
	return ogs[0].UpgradeStrategy() == operatorsv1.UpgradeStrategyUnsafeFailForward, nil
}

type walkOption func(csv *operatorsv1alpha1.ClusterServiceVersion) error

// WithCSVPhase returns an error if the CSV is not in the given phase.
func WithCSVPhase(phase operatorsv1alpha1.ClusterServiceVersionPhase) walkOption {
	return func(csv *operatorsv1alpha1.ClusterServiceVersion) error {
		if csv == nil || csv.Status.Phase != phase {
			return fmt.Errorf("csv %s/%s in phase %s instead of %s", csv.GetNamespace(), csv.GetName(), csv.Status.Phase, phase)
		}
		return nil
	}
}

// WithUniqueCSVs returns an error if the CSV has been seen before.
func WithUniqueCSVs() walkOption {
	visited := map[string]struct{}{}
	return func(csv *operatorsv1alpha1.ClusterServiceVersion) error {
		// Check if we have visited the CSV before
		if _, ok := visited[csv.GetName()]; ok {
			return fmt.Errorf("csv %s/%s has already been seen", csv.GetNamespace(), csv.GetName())
		}

		visited[csv.GetName()] = struct{}{}
		return nil
	}
}

// WalkReplacementChain walks along the chain of clusterServiceVersions being replaced and returns
// the last clusterServiceVersions in the replacement chain. An error is returned if any of the
// clusterServiceVersions before the last is not in the replaces phase or if an infinite replacement
// chain is detected.
func WalkReplacementChain(csv *operatorsv1alpha1.ClusterServiceVersion, csvToReplacement map[string]*operatorsv1alpha1.ClusterServiceVersion, options ...walkOption) (*operatorsv1alpha1.ClusterServiceVersion, error) {
	if csv == nil {
		return nil, fmt.Errorf("csv cannot be nil")
	}

	for {
		// Check if there is a CSV that replaces this CSVs
		next, ok := csvToReplacement[csv.GetName()]
		if !ok {
			break
		}

		// Check walk options
		for _, o := range options {
			if err := o(csv); err != nil {
				return nil, err
			}
		}

		// Move along replacement chain.
		csv = next
	}
	return csv, nil
}

// isReplacementChainThatEndsInFailure returns true if the last CSV in the chain is in the failed phase and all other
// CSVs are in the replacing phase.
func isReplacementChainThatEndsInFailure(csv *operatorsv1alpha1.ClusterServiceVersion, csvToReplacement map[string]*operatorsv1alpha1.ClusterServiceVersion) (bool, error) {
	lastCSV, err := WalkReplacementChain(csv, csvToReplacement, WithCSVPhase(operatorsv1alpha1.CSVPhaseReplacing), WithUniqueCSVs())
	if err != nil {
		return false, err
	}
	return (lastCSV != nil && lastCSV.Status.Phase == operatorsv1alpha1.CSVPhaseFailed), nil
}

// ReplacementMapping takes a list of CSVs and returns a map that maps a CSV's name to the CSV that replaces it.
func ReplacementMapping(csvs []*operatorsv1alpha1.ClusterServiceVersion) map[string]*operatorsv1alpha1.ClusterServiceVersion {
	replacementMapping := map[string]*operatorsv1alpha1.ClusterServiceVersion{}
	for _, csv := range csvs {
		if csv.Spec.Replaces != "" {
			replacementMapping[csv.Spec.Replaces] = csv
		}
	}
	return replacementMapping
}

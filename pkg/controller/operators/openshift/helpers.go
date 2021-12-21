package openshift

import (
	"context"
	"errors"
	"fmt"
	"strings"

	semver "github.com/blang/semver/v4"
	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/projection"
)

func stripObject(obj client.Object) {
	if obj == nil {
		return
	}

	obj.SetResourceVersion("")
	obj.SetUID("")
}

func watchName(name *string) predicate.Funcs {
	return predicate.NewPredicateFuncs(func(object client.Object) bool {
		return object.GetName() == *name
	})
}

func conditionsEqual(a, b *configv1.ClusterOperatorStatusCondition) bool {
	if a == b {
		return true
	}

	if a == nil || b == nil {
		return false
	}

	return a.Type == b.Type && a.Status == b.Status && a.Message == b.Message && a.Reason == b.Reason
}

func versionsMatch(a []configv1.OperandVersion, b []configv1.OperandVersion) bool {
	if len(a) != len(b) {
		return false
	}

	counts := map[configv1.OperandVersion]int{}
	for _, av := range a {
		counts[av]++
	}

	for _, bv := range b {
		remaining, ok := counts[bv]
		if !ok {
			return false
		}

		if remaining == 1 {
			delete(counts, bv)
			continue
		}

		counts[bv]--
	}

	return len(counts) < 1
}

type skews []skew

func (s skews) String() string {
	msg := make([]string, len(s))
	i, j := 0, len(s)-1
	for _, sk := range s {
		m := sk.String()
		// Partial order: error skews first
		if sk.err != nil {
			msg[i] = m
			i++
			continue
		}
		msg[j] = m
		j--
	}

	return "ClusterServiceVersions blocking cluster upgrade: " + strings.Join(msg, ",")
}

type skew struct {
	namespace           string
	name                string
	maxOpenShiftVersion string
	err                 error
}

func (s skew) String() string {
	if s.err != nil {
		return fmt.Sprintf("%s/%s has invalid %s properties: %s", s.namespace, s.name, MaxOpenShiftVersionProperty, s.err)
	}

	return fmt.Sprintf("%s/%s is incompatible with OpenShift minor versions greater than %s", s.namespace, s.name, s.maxOpenShiftVersion)
}

type transientError struct {
	error
}

// transientErrors returns the result of stripping all wrapped errors not of type transientError from the given error.
func transientErrors(err error) error {
	return utilerrors.FilterOut(err, func(e error) bool {
		return !errors.As(e, new(transientError))
	})
}

func incompatibleOperators(ctx context.Context, cli client.Client) (skews, error) {
	desired, err := desiredRelease(ctx, cli)
	if err != nil {
		return nil, err
	}

	if desired == nil {
		// Note: This shouldn't happen
		return nil, fmt.Errorf("failed to determine current OpenShift Y-stream release")
	}

	next, err := nextY(*desired)
	if err != nil {
		return nil, err
	}

	csvList := &operatorsv1alpha1.ClusterServiceVersionList{}
	if err := cli.List(ctx, csvList); err != nil {
		return nil, &transientError{fmt.Errorf("failed to list ClusterServiceVersions: %w", err)}
	}

	var incompatible skews
	for _, csv := range csvList.Items {
		if csv.IsCopied() {
			continue
		}

		s := skew{
			name:      csv.GetName(),
			namespace: csv.GetNamespace(),
		}
		max, err := maxOpenShiftVersion(&csv)
		if err != nil {
			s.err = err
			incompatible = append(incompatible, s)
			continue
		}

		if max == nil || max.GTE(next) {
			continue
		}

		s.maxOpenShiftVersion = fmt.Sprintf("%d.%d", max.Major, max.Minor)

		incompatible = append(incompatible, s)
	}

	return incompatible, nil
}

func desiredRelease(ctx context.Context, cli client.Client) (*semver.Version, error) {
	cv := configv1.ClusterVersion{}
	if err := cli.Get(ctx, client.ObjectKey{Name: "version"}, &cv); err != nil { // "version" is the name of OpenShift's ClusterVersion singleton
		return nil, &transientError{fmt.Errorf("failed to get ClusterVersion: %w", err)}
	}

	v := cv.Status.Desired.Version
	if v == "" {
		// The release version hasn't been set yet
		return nil, fmt.Errorf("desired release version missing from ClusterVersion")
	}

	desired, err := semver.ParseTolerant(v)
	if err != nil {
		return nil, fmt.Errorf("cluster version has invalid desired release version: %w", err)
	}

	return &desired, nil
}

func nextY(v semver.Version) (semver.Version, error) {
	v.Build = nil // Builds are irrelevant

	if len(v.Pre) > 0 {
		// Dropping pre-releases is equivalent to incrementing Y
		v.Pre = nil
		v.Patch = 0

		return v, nil
	}

	return v, v.IncrementMinor() // Sets Y=Y+1 and Z=0
}

const (
	MaxOpenShiftVersionProperty = "olm.maxOpenShiftVersion"
)

func maxOpenShiftVersion(csv *operatorsv1alpha1.ClusterServiceVersion) (*semver.Version, error) {
	// Extract the property from the CSV's annotations if possible
	annotation, ok := csv.GetAnnotations()[projection.PropertiesAnnotationKey]
	if !ok {
		return nil, nil
	}

	properties, err := projection.PropertyListFromPropertiesAnnotation(annotation)
	if err != nil {
		return nil, err
	}

	var max *string
	for _, property := range properties {
		if property.Type != MaxOpenShiftVersionProperty {
			continue
		}

		if max != nil {
			return nil, fmt.Errorf(`defining more than one "%s" property is not allowed`, MaxOpenShiftVersionProperty)
		}

		max = &property.Value
	}

	if max == nil {
		return nil, nil
	}

	// Account for any additional quoting
	value := strings.Trim(*max, "\"")
	if value == "" {
		// Handle "" separately, so parse doesn't treat it as a zero
		return nil, fmt.Errorf(`value cannot be "" (an empty string)`)
	}

	version, err := semver.ParseTolerant(value)
	if err != nil {
		return nil, fmt.Errorf(`failed to parse "%s" as semver: %w`, value, err)
	}

	truncatedVersion := semver.Version{Major: version.Major, Minor: version.Minor}
	if !version.EQ(truncatedVersion) {
		return nil, fmt.Errorf("property %s must specify only <major>.<minor> version, got invalid value %s", MaxOpenShiftVersionProperty, version)
	}
	return &truncatedVersion, nil
}

func notCopiedSelector() (labels.Selector, error) {
	requirement, err := labels.NewRequirement(operatorsv1alpha1.CopiedLabelKey, selection.DoesNotExist, nil)
	if err != nil {
		return nil, err
	}
	return labels.NewSelector().Add(*requirement), nil
}

func olmOperatorRelatedObjects(ctx context.Context, cli client.Client, namespace string) ([]configv1.ObjectReference, error) {
	selector, err := notCopiedSelector()
	if err != nil {
		return nil, err
	}

	csvList := &operatorsv1alpha1.ClusterServiceVersionList{}
	if err := cli.List(ctx, csvList, client.InNamespace(namespace), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, err
	}

	var refs []configv1.ObjectReference
	for _, csv := range csvList.Items {
		if csv.IsCopied() {
			// Filter out copied CSVs that the label selector missed
			continue
		}

		// TODO: Generalize ObjectReference generation
		refs = append(refs, configv1.ObjectReference{
			Group:     operatorsv1alpha1.GroupName,
			Resource:  "clusterserviceversions",
			Namespace: csv.GetNamespace(),
			Name:      csv.GetName(),
		})
	}

	return refs, nil
}

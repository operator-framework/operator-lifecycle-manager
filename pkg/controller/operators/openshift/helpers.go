package openshift

import (
	"context"
	"encoding/json"
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

func watchNamespace(namespace *string) predicate.Funcs {
	return predicate.NewPredicateFuncs(func(object client.Object) bool {
		return object.GetNamespace() == *namespace
	})
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
		counts[av] += 1
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

		counts[bv] -= 1
	}

	return len(counts) < 1
}

type skews []skew

func (s skews) String() string {
	var msg []string
	for _, sk := range s {
		msg = append(msg, sk.String())
	}

	return "The following operators block OpenShift upgrades: " + strings.Join(msg, ",")
}

type skew struct {
	namespace           string
	name                string
	maxOpenShiftVersion string
}

func (s skew) String() string {
	return fmt.Sprintf("Operator %s in namespace %s is not compatible with OpenShift versions greater than %s", s.name, s.namespace, s.maxOpenShiftVersion)
}

func incompatibleOperators(ctx context.Context, cli client.Client) (skews, error) {
	next, err := desiredRelease(ctx, cli)
	if err != nil {
		return nil, err
	}
	next.Minor++

	csvList := &operatorsv1alpha1.ClusterServiceVersionList{}
	if err := cli.List(ctx, csvList); err != nil {
		return nil, err
	}

	var (
		s    skews
		errs []error
	)
	for _, csv := range csvList.Items {
		if csv.IsCopied() {
			continue
		}

		max, err := maxOpenShiftVersion(&csv)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if max == nil || max.GTE(*next) {
			continue
		}

		s = append(s, skew{
			name:                csv.GetName(),
			namespace:           csv.GetNamespace(),
			maxOpenShiftVersion: max.String(),
		})
	}

	return s, utilerrors.NewAggregate(errs)
}

func desiredRelease(ctx context.Context, cli client.Client) (*semver.Version, error) {
	cv := configv1.ClusterVersion{}
	if err := cli.Get(ctx, client.ObjectKey{Name: "version"}, &cv); err != nil { // "version" is the name of OpenShift's ClusterVersion singleton
		return nil, err
	}

	v := cv.Status.Desired.Version
	if v == "" {
		// The release version hasn't been set yet
		return nil, nil
	}

	desired, err := semver.ParseTolerant(v)
	if err != nil {
		return nil, err
	}

	return &desired, nil
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

	// Take the highest semver if there's more than one max version specified
	var (
		max  *semver.Version
		dups []semver.Version
		errs []error
	)
	for _, property := range properties {
		if property.Type != MaxOpenShiftVersionProperty {
			continue
		}

		var value string
		if err := json.Unmarshal([]byte(property.Value), &value); err != nil {
			errs = append(errs, err)
			continue
		}

		if value == "" {
			continue
		}

		version, err := semver.ParseTolerant(value)
		if err != nil {
			errs = append(errs, err)
		}

		if max == nil {
			max = &version
			continue
		}
		if version.LT(*max) {
			continue
		}
		if version.EQ(*max) {
			// Found a duplicate, mark it
			dups = append(dups, *max)
		}

		max = &version
	}

	// Return an error if THE max version has a duplicate (i.e. equivalent version)
	// Note: This may not be a problem since there should be no difference as far as blocking upgrades is concerned.
	// This is more for clear status messages.
	for _, dup := range dups {
		if max.EQ(dup) && max.String() != dup.String() { // "1.0.0" vs "1.0.0" is fine, but not "1.0.0" vs "1.0.0+1"
			errs = append(errs, fmt.Errorf("max openshift version ambiguous, equivalent versions %s and %s have been specified concurrently", max, dup))
		}
	}

	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}

	return max, nil
}

func notCopiedSelector() (labels.Selector, error) {
	requirement, err := labels.NewRequirement(operatorsv1alpha1.CopiedLabelKey, selection.DoesNotExist, nil)
	if err != nil {
		return nil, err
	}

	selector := labels.NewSelector()
	selector.Add(*requirement)

	return selector, nil
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

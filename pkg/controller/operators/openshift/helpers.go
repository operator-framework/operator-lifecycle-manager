package openshift

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

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

	// TODO Revisit this message after 4.23 release once release schedule is determined for future possible minor versions.
	return fmt.Sprintf("ClusterServiceVersions blocking minor version upgrades to 4.23 or major version upgrades to 5.0:\n%s", strings.Join(msg, "\n"))
}

type skew struct {
	namespace           string
	name                string
	maxOpenShiftVersion string
	err                 error
}

func (s skew) String() string {
	if s.err != nil {
		return fmt.Sprintf("- %s/%s has invalid %s properties: %s", s.namespace, s.name, MaxOpenShiftVersionProperty, s.err)
	}
	return fmt.Sprintf("- maximum supported OCP version for %s/%s is %s", s.namespace, s.name, s.maxOpenShiftVersion)
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
	current, err := getCurrentRelease()
	if err != nil {
		return nil, err
	}

	if current == nil {
		// Note: This shouldn't happen
		return nil, fmt.Errorf("failed to determine current OpenShift Y-stream release")
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

		if max == nil || max.GTE(nextY(*current)) {
			continue
		}

		s.maxOpenShiftVersion = fmt.Sprintf("%d.%d", max.Major, max.Minor)

		incompatible = append(incompatible, s)
	}

	return incompatible, nil
}

type openshiftRelease struct {
	version *semver.Version
	mu      sync.Mutex
}

var (
	currentRelease = &openshiftRelease{}
)

const (
	releaseEnvVar = "RELEASE_VERSION" // OpenShift's env variable for defining the current release
)

// getCurrentRelease thread safely retrieves the current version of OCP at the time of this operator starting.
// This is defined by an environment variable that our release manifests define (and get dynamically updated)
// by OCP. For the purposes of this package, that environment variable is a constant under the name of releaseEnvVar.
//
// Note: currentRelease is designed to be a singleton that only gets updated the first time that this function
// is called. As a result, all calls to this will return the same value even if the releaseEnvVar gets
// changed during runtime somehow.
func getCurrentRelease() (*semver.Version, error) {
	currentRelease.mu.Lock()
	defer currentRelease.mu.Unlock()

	if currentRelease.version != nil {
		/*
			If the version is already set, we don't want to set it again as the currentRelease
			is designed to be a singleton. If a new version is set, we are making an assumption
			that this controller will be restarted and thus pull in the new version from the
			environment into memory.

			Note: sync.Once is not used here as it was difficult to reliably test without hitting
			race conditions.
		*/
		return currentRelease.version, nil
	}

	// Get the raw version from the releaseEnvVar environment variable
	raw, ok := os.LookupEnv(releaseEnvVar)
	if !ok || raw == "" {
		// No env var set, try again later
		return nil, fmt.Errorf("desired release version missing from %v env variable", releaseEnvVar)
	}

	release, err := semver.ParseTolerant(raw)
	if err != nil {
		return nil, fmt.Errorf("cluster version has invalid desired release version: %w", err)
	}

	currentRelease.version = &release

	return currentRelease.version, nil
}

func nextY(v semver.Version) semver.Version {
	return semver.Version{Major: v.Major, Minor: v.Minor + 1} // Sets Y=Y+1
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

package resolver

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-registry/pkg/api"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	controllerbundle "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/bundle"
	resolvercache "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/solver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"k8s.io/client-go/tools/cache"
)

var (
	// conventions for tests: packages are letters (a,b,c) and apis are numbers (1,2,3)

	// APISets used for tests
	APISet1   = resolvercache.APISet{testGVKKey: struct{}{}}
	Provides1 = APISet1
	Requires1 = APISet1
	APISet2   = resolvercache.APISet{opregistry.APIKey{Group: "g2", Version: "v2", Kind: "k2", Plural: "k2s"}: struct{}{}}
	Provides2 = APISet2
	Requires2 = APISet2
	APISet3   = resolvercache.APISet{opregistry.APIKey{Group: "g3", Version: "v3", Kind: "k3", Plural: "k3s"}: struct{}{}}
	Provides3 = APISet3
	Requires3 = APISet3
	APISet4   = resolvercache.APISet{opregistry.APIKey{Group: "g4", Version: "v4", Kind: "k4", Plural: "k4s"}: struct{}{}}
	Provides4 = APISet4
	Requires4 = APISet4
)

func TestIsReplacementChainThatEndsInFailure(t *testing.T) {
	type out struct {
		b   bool
		err error
	}

	tests := []struct {
		name             string
		csv              *v1alpha1.ClusterServiceVersion
		csvToReplacement map[string]*v1alpha1.ClusterServiceVersion
		expected         out
	}{
		{
			name:             "NilCSV",
			csv:              nil,
			csvToReplacement: nil,
			expected: out{
				b:   false,
				err: fmt.Errorf("csv cannot be nil"),
			},
		},
		{
			name: "OneEntryReplacementChainEndsInFailure",
			csv: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo-v1",
					Namespace: "bar",
				},
				Status: v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseFailed,
				},
			},
			csvToReplacement: nil,
			expected: out{
				b:   true,
				err: nil,
			},
		},
		{
			name: "OneEntryReplacementChainEndsInSuccess",
			csv: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo-v1",
					Namespace: "bar",
				},
				Status: v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseSucceeded,
				},
			},
			csvToReplacement: nil,
			expected: out{
				b:   false,
				err: nil,
			},
		},
		{
			name: "ReplacementChainEndsInSuccess",
			csv: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo-v1",
					Namespace: "bar",
				},
				Status: v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseReplacing,
				},
			},
			csvToReplacement: map[string]*v1alpha1.ClusterServiceVersion{
				"foo-v1": {
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo-v2",
						Namespace: "bar",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "foo-v1",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: v1alpha1.CSVPhaseReplacing,
					},
				},
				"foo-v2": {
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo-v3",
						Namespace: "bar",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "foo-v2",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: v1alpha1.CSVPhaseSucceeded,
					},
				},
			},
			expected: out{
				b:   false,
				err: nil,
			},
		},
		{
			name: "ReplacementChainEndsInFailure",
			csv: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo-v1",
					Namespace: "bar",
				},
				Status: v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseReplacing,
				},
			},
			csvToReplacement: map[string]*v1alpha1.ClusterServiceVersion{
				"foo-v1": {
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo-v2",
						Namespace: "bar",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "foo-v1",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: v1alpha1.CSVPhaseReplacing,
					},
				},
				"foo-v2": {
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo-v3",
						Namespace: "bar",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "foo-v2",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: v1alpha1.CSVPhaseFailed,
					},
				},
			},
			expected: out{
				b:   true,
				err: nil,
			},
		},
		{
			name: "ReplacementChainBrokenByFailedCSVInMiddle",
			csv: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo-v1",
					Namespace: "bar",
				},
				Status: v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseReplacing,
				},
			},
			csvToReplacement: map[string]*v1alpha1.ClusterServiceVersion{
				"foo-v1": {
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo-v2",
						Namespace: "bar",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "foo-v1",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: v1alpha1.CSVPhaseFailed,
					},
				},
				"foo-v2": {
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo-v3",
						Namespace: "bar",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "foo-v2",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: v1alpha1.CSVPhaseFailed,
					},
				},
			},
			expected: out{
				b:   false,
				err: fmt.Errorf("csv bar/foo-v2 in phase Failed instead of Replacing"),
			},
		},
		{
			name: "InfiniteLoopReplacementChain",
			csv: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo-v1",
					Namespace: "bar",
				},
				Status: v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseReplacing,
				},
			},
			csvToReplacement: map[string]*v1alpha1.ClusterServiceVersion{
				"foo-v1": {
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo-v2",
						Namespace: "bar",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "foo-v1",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: v1alpha1.CSVPhaseReplacing,
					},
				},
				"foo-v2": {
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo-v1",
						Namespace: "bar",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "foo-v2",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: v1alpha1.CSVPhaseReplacing,
					},
				},
			},
			expected: out{
				b:   false,
				err: fmt.Errorf("csv bar/foo-v1 has already been seen"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endsInFailure, err := isReplacementChainThatEndsInFailure(tt.csv, tt.csvToReplacement)
			require.Equal(t, tt.expected.b, endsInFailure)
			require.Equal(t, tt.expected.err, err)
		})
	}
}

func TestInitHooks(t *testing.T) {
	clientFake := fake.NewSimpleClientset()
	lister := operatorlister.NewLister()
	log := logrus.New()

	// no init hooks
	resolver := NewOperatorStepResolver(lister, clientFake, "", nil, log)
	require.NotNil(t, resolver.resolver)

	// with init hook
	var testHook stepResolverInitHook = func(resolver *OperatorStepResolver) error {
		resolver.resolver = nil
		return nil
	}

	// defined in step_resolver.go
	initHooks = append(initHooks, testHook)
	defer func() {
		// reset initHooks
		initHooks = nil
	}()

	resolver = NewOperatorStepResolver(lister, clientFake, "", nil, log)
	require.Nil(t, resolver.resolver)
}

func TestResolver(t *testing.T) {
	const namespace = "catsrc-namespace"
	catalog := resolvercache.SourceKey{Name: "catsrc", Namespace: namespace}

	type resolverTestOut struct {
		steps       [][]*v1alpha1.Step
		lookups     []v1alpha1.BundleLookup
		subs        []*v1alpha1.Subscription
		errAssert   func(*testing.T, error)
		solverError solver.NotSatisfiable
	}
	type resolverTest struct {
		name             string
		clusterState     []runtime.Object
		bundlesByCatalog map[resolvercache.SourceKey][]*api.Bundle
		out              resolverTestOut
	}

	nothing := resolverTestOut{
		steps:   [][]*v1alpha1.Step{},
		lookups: []v1alpha1.BundleLookup{},
		subs:    []*v1alpha1.Subscription{},
	}
	tests := []resolverTest{
		{
			name: "SubscriptionOmitsChannel",
			clusterState: []runtime.Object{
				newSub(namespace, "package", "", catalog),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("bundle", "package", "channel", "", nil, nil, nil, nil),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("bundle", "package", "channel", "", nil, nil, nil, nil), namespace, "", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "bundle", "", "package", "", catalog),
				},
			},
		},
		{
			name: "SubscriptionWithNoCandidates/Error",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
				newOperatorGroup("foo", namespace),
			},
			out: resolverTestOut{
				solverError: solver.NotSatisfiable{
					{
						Variable:   NewSubscriptionVariable("a", nil),
						Constraint: PrettyConstraint(solver.Mandatory(), "subscription a-alpha exists"),
					},
					{
						Variable:   NewSubscriptionVariable("a", nil),
						Constraint: PrettyConstraint(solver.Dependency(), "no operators found from catalog catsrc in namespace catsrc-namespace referenced by subscription a-alpha"),
					},
				},
			},
		},
		{
			name: "SubscriptionWithNoCandidatesInPackage/Error",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("bundle", "package", "channel", "", nil, nil, nil, nil),
				},
			},
			out: resolverTestOut{
				solverError: solver.NotSatisfiable{
					{
						Variable:   NewSubscriptionVariable("a", nil),
						Constraint: PrettyConstraint(solver.Mandatory(), "subscription a-alpha exists"),
					},
					{
						Variable:   NewSubscriptionVariable("a", nil),
						Constraint: PrettyConstraint(solver.Dependency(), "no operators found in package a in the catalog referenced by subscription a-alpha"),
					},
				},
			},
		},
		{
			name: "SubscriptionWithNoCandidatesInChannel/Error",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("bundle", "a", "channel", "", nil, nil, nil, nil),
				},
			},
			out: resolverTestOut{
				solverError: solver.NotSatisfiable{
					{
						Variable:   NewSubscriptionVariable("a", nil),
						Constraint: PrettyConstraint(solver.Mandatory(), "subscription a-alpha exists"),
					},
					{
						Variable:   NewSubscriptionVariable("a", nil),
						Constraint: PrettyConstraint(solver.Dependency(), "no operators found in channel alpha of package a in the catalog referenced by subscription a-alpha"),
					},
				},
			},
		},
		{
			name: "SubscriptionWithNoCandidatesWithStartingCSVName/Error",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog, withStartingCSV("notfound")),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("bundle", "a", "alpha", "", nil, nil, nil, nil),
				},
			},
			out: resolverTestOut{
				solverError: solver.NotSatisfiable{
					{
						Variable:   NewSubscriptionVariable("a", nil),
						Constraint: PrettyConstraint(solver.Mandatory(), "subscription a-alpha exists"),
					},
					{
						Variable:   NewSubscriptionVariable("a", nil),
						Constraint: PrettyConstraint(solver.Dependency(), "no operators found with name notfound in channel alpha of package a in the catalog referenced by subscription a-alpha"),
					},
				},
			},
		},
		{
			name: "SingleNewSubscription/NoDeps",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", nil, nil, nil, nil),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v1", "a", "alpha", "", nil, nil, nil, nil), namespace, "", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v1", "", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "SingleNewSubscription/ResolveOne",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("b.v1", "b", "beta", "", Provides1, nil, nil, nil),
					bundle("a.v1", "a", "alpha", "", nil, Requires1, nil, nil),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v1", "a", "alpha", "", nil, Requires1, nil, nil), namespace, "", catalog),
					bundleSteps(bundle("b.v1", "b", "beta", "", Provides1, nil, nil, nil), namespace, "", catalog),
					subSteps(namespace, "b.v1", "b", "beta", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v1", "", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "SingleNewSubscription/ResolveOne/BundlePath",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", nil, Requires1, nil, nil),
					stripManifests(withBundlePath(bundle("b.v1", "b", "beta", "", Provides1, nil, nil, nil), "quay.io/test/bundle@sha256:abcd")),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v1", "a", "alpha", "", nil, Requires1, nil, nil), namespace, "", catalog),
					subSteps(namespace, "b.v1", "b", "beta", catalog),
				},
				lookups: []v1alpha1.BundleLookup{
					{
						Path:       "quay.io/test/bundle@sha256:abcd",
						Identifier: "b.v1",
						Properties: `{"properties":[{"type":"olm.gvk","value":{"group":"g","kind":"k","version":"v"}},{"type":"olm.package","value":{"packageName":"b","version":"0.0.0"}}]}`,
						CatalogSourceRef: &corev1.ObjectReference{
							Namespace: catalog.Namespace,
							Name:      catalog.Name,
						},
						Conditions: []v1alpha1.BundleLookupCondition{
							{
								Type:    BundleLookupConditionPacked,
								Status:  corev1.ConditionTrue,
								Reason:  controllerbundle.NotUnpackedReason,
								Message: controllerbundle.NotUnpackedMessage,
							},
							{
								Type:    v1alpha1.BundleLookupPending,
								Status:  corev1.ConditionTrue,
								Reason:  controllerbundle.JobNotStartedReason,
								Message: controllerbundle.JobNotStartedMessage,
							},
						},
					},
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v1", "", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "SingleNewSubscription/ResolveOne/AdditionalBundleObjects",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					withBundleObject(bundle("b.v1", "b", "beta", "", Provides1, nil, nil, nil), u(&rbacv1.RoleBinding{TypeMeta: metav1.TypeMeta{Kind: "RoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"}, ObjectMeta: metav1.ObjectMeta{Name: "test-rb"}})),
					bundle("a.v1", "a", "alpha", "", nil, Requires1, nil, nil),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v1", "a", "alpha", "", nil, Requires1, nil, nil), namespace, "", catalog),
					bundleSteps(withBundleObject(bundle("b.v1", "b", "beta", "", Provides1, nil, nil, nil), u(&rbacv1.RoleBinding{TypeMeta: metav1.TypeMeta{Kind: "RoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"}, ObjectMeta: metav1.ObjectMeta{Name: "test-rb"}})), namespace, "", catalog),
					subSteps(namespace, "b.v1", "b", "beta", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v1", "", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "SingleNewSubscription/ResolveOne/AdditionalBundleObjects/Service",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					withBundleObject(bundle("b.v1", "b", "beta", "", Provides1, nil, nil, nil), u(&corev1.Service{TypeMeta: metav1.TypeMeta{Kind: "Service", APIVersion: ""}, ObjectMeta: metav1.ObjectMeta{Name: "test-service"}})),
					bundle("a.v1", "a", "alpha", "", nil, Requires1, nil, nil),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v1", "a", "alpha", "", nil, Requires1, nil, nil), namespace, "", catalog),
					bundleSteps(withBundleObject(bundle("b.v1", "b", "beta", "", Provides1, nil, nil, nil), u(&corev1.Service{TypeMeta: metav1.TypeMeta{Kind: "Service", APIVersion: ""}, ObjectMeta: metav1.ObjectMeta{Name: "test-service"}})), namespace, "", catalog),
					subSteps(namespace, "b.v1", "b", "beta", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v1", "", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "SingleNewSubscription/DependencyMissing",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", nil, Requires1, nil, nil),
				},
			},
			out: resolverTestOut{
				steps:   [][]*v1alpha1.Step{},
				lookups: []v1alpha1.BundleLookup{},
				subs:    []*v1alpha1.Subscription{},
				solverError: solver.NotSatisfiable([]solver.AppliedConstraint{
					{
						Variable:   NewSubscriptionVariable("a", []solver.Identifier{"catsrc/catsrc-namespace/alpha/a.v1"}),
						Constraint: PrettyConstraint(solver.Dependency("catsrc/catsrc-namespace/alpha/a.v1"), "subscription a-alpha requires catsrc/catsrc-namespace/alpha/a.v1"),
					},
					{
						Variable: &BundleVariable{
							identifier:  "catsrc/catsrc-namespace/alpha/a.v1",
							constraints: []solver.Constraint{solver.Dependency()},
						},
						Constraint: PrettyConstraint(solver.Dependency(), "bundle a.v1 requires an operator providing an API with group: g, version: v, kind: k"),
					},
					{
						Variable:   NewSubscriptionVariable("a", []solver.Identifier{"catsrc/catsrc-namespace/alpha/a.v1"}),
						Constraint: PrettyConstraint(solver.Mandatory(), "subscription a-alpha exists"),
					},
				}),
			},
		},
		{
			name: "InstalledSub/NoUpdates",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
				},
			},
			out: nothing,
		},
		{
			name: "SecondSubscriptionConflictsWithExistingResolvedSubscription",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingSub(namespace, "b.v1", "b", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
					bundle("b.v1", "b", "alpha", "", Provides1, nil, nil, nil),
				},
			},
			out: resolverTestOut{
				errAssert: func(t *testing.T, err error) {
					assert.IsType(t, solver.NotSatisfiable{}, err)
				},
			},
		},
		{
			name: "ConflictingSubscriptionsToSamePackage",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
				newSub(namespace, "a", "beta", catalog),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
					bundle("a.v2", "a", "beta", "", Provides1, nil, nil, nil),
				},
			},
			out: resolverTestOut{
				errAssert: func(t *testing.T, err error) {
					fmt.Println(err)
					assert.IsType(t, solver.NotSatisfiable{}, err)
				},
			},
		},
		{
			// No two operators from the same package may run at the same time, but it's possible to have two
			// subscriptions to the same package as long as it's possible to find a bundle that satisfies both
			// constraints
			name: "SatisfiableSubscriptionsToSamePackage",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
				func() (s *v1alpha1.Subscription) {
					s = newSub(namespace, "a", "alpha", catalog)
					s.Name = s.Name + "-2"
					return
				}(),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil), namespace, "", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v1", "", "a", "alpha", catalog),
					func() (s *v1alpha1.Subscription) {
						s = updatedSub(namespace, "a.v1", "", "a", "alpha", catalog)
						s.Name = s.Name + "-2"
						return
					}(),
				},
			},
		},
		{
			name: "TwoExistingOperatorsWithSameName/NoError",
			clusterState: []runtime.Object{
				existingOperator("ns1", "a.v1", "a", "alpha", "", nil, nil, nil, nil),
				existingOperator("ns2", "a.v1", "a", "alpha", "", nil, nil, nil, nil),
				newOperatorGroup("foo", namespace),
			},
			out: nothing,
		},
		{
			name: "InstalledSub/UpdateAvailable",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v2", "a", "alpha", "a.v1", Provides1, nil, nil, nil),
					bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v2", "a", "alpha", "a.v1", Provides1, nil, nil, nil), namespace, "", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v2", "a.v1", "a", "alpha", catalog),
				},
			},
		},
		{
			// Tests the migration from one package name to another with replaces.
			// Useful when renaming a package or combining two packages into one.
			name: "InstalledSub/UpdateAvailable/FromDifferentPackage",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "b", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
					bundle("b.v2", "b", "alpha", "a.v1", Provides1, nil, nil, nil),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("b.v2", "b", "alpha", "a.v1", Provides1, nil, nil, nil), namespace, "", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "b.v2", "a.v1", "b", "alpha", catalog),
				},
			},
		},
		{
			name: "InstalledSub/UpdateAvailable/FromBundlePath",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{catalog: {
				stripManifests(withBundlePath(bundle("a.v2", "a", "alpha", "a.v1", Provides1, nil, nil, nil), "quay.io/test/bundle@sha256:abcd"))},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{},
				lookups: []v1alpha1.BundleLookup{
					{
						Path:       "quay.io/test/bundle@sha256:abcd",
						Identifier: "a.v2",
						Replaces:   "a.v1",
						Properties: `{"properties":[{"type":"olm.gvk","value":{"group":"g","kind":"k","version":"v"}},{"type":"olm.package","value":{"packageName":"a","version":"0.0.0"}}]}`,
						CatalogSourceRef: &corev1.ObjectReference{
							Namespace: catalog.Namespace,
							Name:      catalog.Name,
						},
						Conditions: []v1alpha1.BundleLookupCondition{
							{
								Type:    BundleLookupConditionPacked,
								Status:  corev1.ConditionTrue,
								Reason:  controllerbundle.NotUnpackedReason,
								Message: controllerbundle.NotUnpackedMessage,
							},
							{
								Type:    v1alpha1.BundleLookupPending,
								Status:  corev1.ConditionTrue,
								Reason:  controllerbundle.JobNotStartedReason,
								Message: controllerbundle.JobNotStartedMessage,
							},
						},
					},
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v2", "a.v1", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "InstalledSub/NoRunningOperator",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil), namespace, "", catalog),
				},
				// no updated subs because existingSub already points the right CSV, it just didn't exist for some reason
				subs: []*v1alpha1.Subscription{},
			},
		},
		{
			name: "InstalledSub/UpdateFound/UpdateRequires/ResolveOne",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", nil, nil, nil, nil),
					bundle("a.v2", "a", "alpha", "a.v1", nil, Requires1, nil, nil),
					bundle("b.v1", "b", "beta", "", Provides1, nil, nil, nil),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v2", "a", "alpha", "a.v1", nil, Requires1, nil, nil), namespace, "", catalog),
					bundleSteps(bundle("b.v1", "b", "beta", "", Provides1, nil, nil, nil), namespace, "", catalog),
					subSteps(namespace, "b.v1", "b", "beta", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v2", "a.v1", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "InstalledSub/UpdateFound/UpdateRequires/ResolveOne/APIServer",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", nil, nil, Provides1, nil),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", nil, nil, nil, nil),
					bundle("a.v2", "a", "alpha", "a.v1", nil, nil, nil, Requires1),
					bundle("b.v1", "b", "beta", "", nil, nil, Provides1, nil),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v2", "a", "alpha", "a.v1", nil, nil, nil, Requires1), namespace, "", catalog),
					bundleSteps(bundle("b.v1", "b", "beta", "", nil, nil, Provides1, nil), namespace, "", catalog),
					subSteps(namespace, "b.v1", "b", "beta", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v2", "a.v1", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "InstalledSub/SingleNewSubscription/UpdateAvailable/ResolveOne",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
				newSub(namespace, "b", "beta", catalog),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", nil, nil, nil, nil),
					bundle("a.v2", "a", "alpha", "a.v1", nil, nil, nil, nil),
					bundle("b.v1", "b", "beta", "", nil, nil, nil, nil),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v2", "a", "alpha", "a.v1", nil, nil, nil, nil), namespace, "", catalog),
					bundleSteps(bundle("b.v1", "b", "beta", "", nil, nil, nil, nil), namespace, "", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v2", "a.v1", "a", "alpha", catalog),
					updatedSub(namespace, "b.v1", "", "b", "beta", catalog),
				},
			},
		},
		{
			name: "InstalledSub/SingleNewSubscription/NoRunningOperator/ResolveOne",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				newSub(namespace, "b", "beta", catalog),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
					bundle("b.v1", "b", "beta", "", nil, nil, nil, nil),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil), namespace, "", catalog),
					bundleSteps(bundle("b.v1", "b", "beta", "", nil, nil, nil, nil), namespace, "", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "b.v1", "", "b", "beta", catalog),
				},
			},
		},
		{
			name: "InstalledSub/SingleNewSubscription/NoRunningOperator/ResolveOne/APIServer",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				newSub(namespace, "b", "beta", catalog),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", nil, nil, Provides1, nil),
					bundle("b.v1", "b", "beta", "", nil, nil, nil, nil),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v1", "a", "alpha", "", nil, nil, Provides1, nil), namespace, "", catalog),
					bundleSteps(bundle("b.v1", "b", "beta", "", nil, nil, nil, nil), namespace, "", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "b.v1", "", "b", "beta", catalog),
				},
			},
		},
		{
			// This test verifies that version deadlock that could happen with the previous algorithm can't happen here
			name: "NoMoreVersionDeadlock",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, Requires2, nil, nil),
				existingSub(namespace, "b.v1", "b", "alpha", catalog),
				existingOperator(namespace, "b.v1", "b", "alpha", "", Provides2, Requires1, nil, nil),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v2", "a", "alpha", "a.v1", Provides3, Requires4, nil, nil),
					bundle("b.v2", "b", "alpha", "b.v1", Provides4, Requires3, nil, nil),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v2", "a", "alpha", "a.v1", Provides3, Requires4, nil, nil), namespace, "", catalog),
					bundleSteps(bundle("b.v2", "b", "alpha", "b.v1", Provides4, Requires3, nil, nil), namespace, "", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v2", "a.v1", "a", "alpha", catalog),
					updatedSub(namespace, "b.v2", "b.v1", "b", "alpha", catalog),
				},
			},
		},
		{
			// This test verifies that ownership of an api can be migrated between two operators
			name: "OwnedAPITransfer",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
				existingSub(namespace, "b.v1", "b", "alpha", catalog),
				existingOperator(namespace, "b.v1", "b", "alpha", "", nil, Requires1, nil, nil),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v2", "a", "alpha", "a.v1", nil, nil, nil, nil),
					bundle("b.v2", "b", "alpha", "b.v1", Provides1, nil, nil, nil),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v2", "a", "alpha", "a.v1", nil, nil, nil, nil), namespace, "", catalog),
					bundleSteps(bundle("b.v2", "b", "alpha", "b.v1", Provides1, nil, nil, nil), namespace, "", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v2", "a.v1", "a", "alpha", catalog),
					updatedSub(namespace, "b.v2", "b.v1", "b", "alpha", catalog),
				},
			},
		},
		{
			name: "PicksOlderProvider",
			clusterState: []runtime.Object{
				newSub(namespace, "b", "alpha", catalog),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
					bundle("a.v2", "a", "alpha", "a.v1", nil, nil, nil, nil),
					bundle("b.v1", "b", "alpha", "", nil, Requires1, nil, nil),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil), namespace, "", catalog),
					bundleSteps(bundle("b.v1", "b", "alpha", "", nil, Requires1, nil, nil), namespace, "", catalog),
					subSteps(namespace, "a.v1", "a", "alpha", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "b.v1", "", "b", "alpha", catalog),
				},
			},
		},
		{
			name: "InstalledSub/UpdateInHead/SkipRange",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{catalog: {
				bundle("a.v3", "a", "alpha", "a.v2", nil, nil, nil, nil, withVersion("1.0.0"), withSkipRange("< 1.0.0")),
			}},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v3", "a", "alpha", "a.v2", nil, nil, nil, nil, withVersion("1.0.0"), withSkipRange("< 1.0.0")), namespace, "a.v1", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v3", "a.v1", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "InstalledSubs/ExistingOperators/OldCSVsReplaced",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingSub(namespace, "b.v1", "b", "beta", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", nil, Requires1, nil, nil),
				existingOperator(namespace, "b.v1", "b", "beta", "", Provides1, nil, nil, nil),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", nil, Requires1, nil, nil),
					bundle("a.v2", "a", "alpha", "a.v1", nil, Requires1, nil, nil),
					bundle("b.v1", "b", "beta", "", Provides1, nil, nil, nil),
					bundle("b.v2", "b", "beta", "b.v1", Provides1, nil, nil, nil),
				},
			},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v2", "a", "alpha", "a.v1", nil, Requires1, nil, nil), namespace, "", catalog),
					bundleSteps(bundle("b.v2", "b", "beta", "b.v1", Provides1, nil, nil, nil), namespace, "", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v2", "a.v1", "a", "alpha", catalog),
					updatedSub(namespace, "b.v2", "b.v1", "b", "beta", catalog),
				},
			},
		},
		{
			name: "InstalledSub/UpdatesAvailable/SkipRangeNotInHead",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{catalog: {
				bundle("a.v2", "a", "alpha", "", nil, nil, nil, nil, withVersion("1.0.0"), withSkipRange("< 1.0.0")),
				bundle("a.v3", "a", "alpha", "a.v2", nil, nil, nil, nil, withVersion("1.0.0"), withSkipRange("< 1.0.0")),
				bundle("a.v4", "a", "alpha", "a.v3", nil, nil, nil, nil, withVersion("1.0.0"), withSkipRange("< 1.0.0 !0.0.0")),
			}},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v3", "a", "alpha", "", nil, nil, nil, nil, withVersion("1.0.0"), withSkipRange("< 1.0.0")), namespace, "a.v1", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v3", "a.v1", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "NewSub/StartingCSV",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog, withStartingCSV("a.v2")),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{catalog: {
				bundle("a.v1", "a", "alpha", "", nil, nil, nil, nil),
				bundle("a.v2", "a", "alpha", "a.v1", nil, nil, nil, nil),
				bundle("a.v3", "a", "alpha", "a.v2", nil, nil, nil, nil, withVersion("1.0.0"), withSkipRange("< 1.0.0")),
			}},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v2", "a", "alpha", "a.v1", nil, nil, nil, nil), namespace, "a.v1", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v2", "", "a", "alpha", catalog, withStartingCSV("a.v2")),
				},
			},
		},
		{
			name: "InstalledSub/UpdatesAvailable/SpecifiedSkips",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{catalog: {
				bundle("a.v2", "a", "alpha", "", nil, nil, nil, nil, withVersion("1.0.0"), withSkips([]string{"a.v1"})),
				bundle("a.v3", "a", "alpha", "a.v2", nil, nil, nil, nil, withVersion("1.0.0"), withSkips([]string{"a.v1"})),
			}},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v3", "a", "alpha", "", nil, nil, nil, nil, withVersion("1.0.0")), namespace, "a.v1", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v3", "a.v1", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "FailForwardDisabled/2EntryReplacementChain/NotSatisfiable",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v2", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil, withPhase(v1alpha1.CSVPhaseReplacing)),
				existingOperator(namespace, "a.v2", "a", "alpha", "a.v1", Provides1, nil, nil, nil, withPhase(v1alpha1.CSVPhaseFailed)),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{catalog: {
				bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil, withVersion("1.0.0")),
				bundle("a.v2", "a", "alpha", "a.v1", Provides1, nil, nil, nil, withVersion("2.0.0")),
				bundle("a.v3", "a", "alpha", "a.v2", Provides1, nil, nil, nil, withVersion("3.0.0")),
			}},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{},
				subs:  []*v1alpha1.Subscription{},
				errAssert: func(t *testing.T, err error) {
					assert.IsType(t, solver.NotSatisfiable{}, err)
					assert.Contains(t, err.Error(), "constraints not satisfiable")
					assert.Contains(t, err.Error(), "provide k (g/v)")
					assert.Contains(t, err.Error(), "clusterserviceversion a.v1 exists and is not referenced by a subscription")
					assert.Contains(t, err.Error(), "subscription a-alpha requires at least one of catsrc/catsrc-namespace/alpha/a.v3 or @existing/catsrc-namespace//a.v2")
				},
			},
		},
		{
			name: "FailForwardEnabled/2EntryReplacementChain/Satisfiable",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v2", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil, withPhase(v1alpha1.CSVPhaseReplacing)),
				existingOperator(namespace, "a.v2", "a", "alpha", "a.v1", Provides1, nil, nil, nil, withPhase(v1alpha1.CSVPhaseFailed)),
				newOperatorGroup("foo", namespace, withUpgradeStrategy(operatorsv1.UpgradeStrategyUnsafeFailForward)),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{catalog: {
				bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil, withVersion("1.0.0")),
				bundle("a.v2", "a", "alpha", "a.v1", Provides1, nil, nil, nil, withVersion("2.0.0")),
				bundle("a.v3", "a", "alpha", "a.v2", Provides1, nil, nil, nil, withVersion("3.0.0")),
			}},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v3", "a", "alpha", "a.v2", Provides1, nil, nil, nil, withVersion("3.0.0")), namespace, "a.v2", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v3", "a.v2", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "FailForwardDisabled/3EntryReplacementChain/NotSatisfiable",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v3", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil, withPhase(v1alpha1.CSVPhaseReplacing)),
				existingOperator(namespace, "a.v2", "a", "alpha", "a.v1", Provides1, nil, nil, nil, withPhase(v1alpha1.CSVPhaseReplacing)),
				existingOperator(namespace, "a.v3", "a", "alpha", "a.v2", Provides1, nil, nil, nil, withPhase(v1alpha1.CSVPhaseFailed)),
				newOperatorGroup("foo", namespace),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{catalog: {
				bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil, withVersion("1.0.0")),
				bundle("a.v2", "a", "alpha", "a.v1", Provides1, nil, nil, nil, withVersion("2.0.0")),
				bundle("a.v3", "a", "alpha", "a.v2", Provides1, nil, nil, nil, withVersion("3.0.0")),
				bundle("a.v4", "a", "alpha", "a.v3", Provides1, nil, nil, nil, withVersion("4.0.0")),
			}},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{},
				subs:  []*v1alpha1.Subscription{},
				errAssert: func(t *testing.T, err error) {
					assert.IsType(t, solver.NotSatisfiable{}, err)
					assert.Contains(t, err.Error(), "constraints not satisfiable")
					assert.Contains(t, err.Error(), "provide k (g/v)")
					assert.Contains(t, err.Error(), "exists and is not referenced by a subscription")
				},
			},
		},
		{
			name: "FailForwardEnabled/3EntryReplacementChain/Satisfiable",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v3", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil, withPhase(v1alpha1.CSVPhaseReplacing)),
				existingOperator(namespace, "a.v2", "a", "alpha", "a.v1", Provides1, nil, nil, nil, withPhase(v1alpha1.CSVPhaseReplacing)),
				existingOperator(namespace, "a.v3", "a", "alpha", "a.v2", Provides1, nil, nil, nil, withPhase(v1alpha1.CSVPhaseFailed)),
				newOperatorGroup("foo", namespace, withUpgradeStrategy(operatorsv1.UpgradeStrategyUnsafeFailForward)),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{catalog: {
				bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil, withVersion("1.0.0")),
				bundle("a.v2", "a", "alpha", "a.v1", Provides1, nil, nil, nil, withVersion("2.0.0")),
				bundle("a.v3", "a", "alpha", "a.v2", Provides1, nil, nil, nil, withVersion("3.0.0")),
				bundle("a.v4", "a", "alpha", "a.v3", Provides1, nil, nil, nil, withVersion("4.0.0")),
			}},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v4", "a", "alpha", "a.v3", Provides1, nil, nil, nil, withVersion("4.0.0")), namespace, "a.v3", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v4", "a.v3", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "FailForwardEnabled/3EntryReplacementChain/ReplacementChainBroken/NotSatisfiable",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v3", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil, withPhase(v1alpha1.CSVPhaseFailed)),
				existingOperator(namespace, "a.v2", "a", "alpha", "a.v1", Provides1, nil, nil, nil, withPhase(v1alpha1.CSVPhaseFailed)),
				existingOperator(namespace, "a.v3", "a", "alpha", "a.v2", Provides1, nil, nil, nil, withPhase(v1alpha1.CSVPhaseFailed)),
				newOperatorGroup("foo", namespace, withUpgradeStrategy(operatorsv1.UpgradeStrategyUnsafeFailForward)),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{catalog: {
				bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil, withVersion("1.0.0")),
				bundle("a.v2", "a", "alpha", "a.v1", Provides1, nil, nil, nil, withVersion("2.0.0")),
				bundle("a.v3", "a", "alpha", "a.v2", Provides1, nil, nil, nil, withVersion("3.0.0")),
				bundle("a.v4", "a", "alpha", "a.v3", Provides1, nil, nil, nil, withVersion("4.0.0")),
			}},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{},
				subs:  []*v1alpha1.Subscription{},
				errAssert: func(t *testing.T, err error) {
					assert.Contains(t, err.Error(), "error using catalogsource catsrc-namespace/@existing: csv")
					assert.Contains(t, err.Error(), "in phase Failed instead of Replacing")
				},
			},
		},
		{
			name: "FailForwardEnabled/MultipleReplaces/ReplacementChainEndsInFailure/ConflictingProvider/NoUpgrade",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "b.v1", "b", "alpha", "", Provides1, nil, nil, nil, withPhase(v1alpha1.CSVPhaseReplacing)),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil, withPhase(v1alpha1.CSVPhaseFailed)),
				newOperatorGroup("foo", namespace, withUpgradeStrategy(operatorsv1.UpgradeStrategyUnsafeFailForward)),
			},
			bundlesByCatalog: map[resolvercache.SourceKey][]*api.Bundle{catalog: {
				bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil, withVersion("1.0.0")),
				bundle("a.v2", "a", "alpha", "a.v1", Provides1, nil, nil, nil, withVersion("2.0.0")),
				bundle("b.v1", "b", "alpha", "", Provides1, nil, nil, nil, withVersion("1.0.0")),
			}},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{},
				subs:  []*v1alpha1.Subscription{},
				errAssert: func(t *testing.T, err error) {
					assert.IsType(t, solver.NotSatisfiable{}, err)
					assert.Contains(t, err.Error(), "constraints not satisfiable")
					assert.Contains(t, err.Error(), "provide k (g/v)")
					assert.Contains(t, err.Error(), "clusterserviceversion b.v1 exists and is not referenced by a subscription")
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stopc := make(chan struct{})
			defer func() {
				stopc <- struct{}{}
			}()
			expectedSteps := []*v1alpha1.Step{}
			for _, steps := range tt.out.steps {
				expectedSteps = append(expectedSteps, steps...)
			}
			clientFake, informerFactory, _ := StartResolverInformers(namespace, stopc, tt.clusterState...)
			lister := operatorlister.NewLister()
			lister.OperatorsV1alpha1().RegisterSubscriptionLister(namespace, informerFactory.Operators().V1alpha1().Subscriptions().Lister())
			lister.OperatorsV1alpha1().RegisterClusterServiceVersionLister(namespace, informerFactory.Operators().V1alpha1().ClusterServiceVersions().Lister())
			lister.OperatorsV1().RegisterOperatorGroupLister(namespace, informerFactory.Operators().V1().OperatorGroups().Lister())

			ssp := make(resolvercache.StaticSourceProvider)
			for catalog, bundles := range tt.bundlesByCatalog {
				snapshot := &resolvercache.Snapshot{}
				for _, bundle := range bundles {
					op, err := newOperatorFromBundle(bundle, "", catalog, "")
					if err != nil {
						t.Fatalf("unexpected error: %v", err)
					}
					snapshot.Entries = append(snapshot.Entries, op)
				}
				ssp[catalog] = snapshot
			}
			log := logrus.New()
			ssp[resolvercache.NewVirtualSourceKey(namespace)] = &csvSource{
				key:       resolvercache.NewVirtualSourceKey(namespace),
				csvLister: lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(namespace),
				subLister: lister.OperatorsV1alpha1().SubscriptionLister().Subscriptions(namespace),
				ogLister:  lister.OperatorsV1().OperatorGroupLister().OperatorGroups(namespace),
				listSubscriptions: func(ctx context.Context) (*v1alpha1.SubscriptionList, error) {
					items, err := lister.OperatorsV1alpha1().SubscriptionLister().Subscriptions(namespace).List(labels.Everything())
					if err != nil {
						return nil, err
					}
					var out []v1alpha1.Subscription
					for _, sub := range items {
						out = append(out, *sub)
					}
					return &v1alpha1.SubscriptionList{
						Items: out,
					}, nil
				},
				logger: log,
			}
			satresolver := &Resolver{
				cache: resolvercache.New(ssp),
				log:   log,
			}
			resolver := NewOperatorStepResolver(lister, clientFake, "", nil, log)
			resolver.resolver = satresolver

			steps, lookups, subs, err := resolver.ResolveSteps(namespace)
			if tt.out.solverError == nil {
				if tt.out.errAssert == nil {
					assert.NoError(t, err)
				} else {
					tt.out.errAssert(t, err)
				}
			} else {
				// the solver outputs useful information on a failed resolution, which is different from the old resolver
				require.NotNil(t, err)
				expectedStrings := []string{}
				for _, e := range tt.out.solverError {
					expectedStrings = append(expectedStrings, e.String())
				}
				actualStrings := []string{}
				for _, e := range err.(solver.NotSatisfiable) {
					actualStrings = append(actualStrings, e.String())
				}
				require.ElementsMatch(t, expectedStrings, actualStrings)
			}
			requireStepsEqual(t, expectedSteps, steps)
			require.ElementsMatch(t, tt.out.lookups, lookups)
			require.ElementsMatch(t, tt.out.subs, subs)
		})
	}
}

func TestNamespaceResolverRBAC(t *testing.T) {
	namespace := "catsrc-namespace"
	catalog := resolvercache.SourceKey{Name: "catsrc", Namespace: namespace}

	simplePermissions := []v1alpha1.StrategyDeploymentPermissions{
		{
			ServiceAccountName: "test-sa",
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get", "list"},
					APIGroups: []string{""},
					Resources: []string{"configmaps"},
				},
			},
		},
	}
	bundle := bundleWithPermissions("a.v1", "a", "alpha", "", nil, nil, nil, nil, simplePermissions, simplePermissions)
	defaultServiceAccountPermissions := []v1alpha1.StrategyDeploymentPermissions{
		{
			ServiceAccountName: "default",
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get", "list"},
					APIGroups: []string{""},
					Resources: []string{"configmaps"},
				},
			},
		},
	}
	bundleWithDefaultServiceAccount := bundleWithPermissions("a.v1", "a", "alpha", "", nil, nil, nil, nil, defaultServiceAccountPermissions, defaultServiceAccountPermissions)
	type out struct {
		steps [][]*v1alpha1.Step
		subs  []*v1alpha1.Subscription
		err   error
	}
	tests := []struct {
		name               string
		clusterState       []runtime.Object
		bundlesInCatalog   []*api.Bundle
		failForwardEnabled bool
		out                out
	}{
		{
			name: "NewSubscription/Permissions/ClusterPermissions",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
				newOperatorGroup("test-og", namespace),
			},
			bundlesInCatalog: []*api.Bundle{bundle},
			out: out{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle, namespace, "", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v1", "", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "don't create default service accounts",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
				newOperatorGroup("test-og", namespace),
			},
			bundlesInCatalog: []*api.Bundle{bundleWithDefaultServiceAccount},
			out: out{
				steps: [][]*v1alpha1.Step{
					withoutResourceKind("ServiceAccount", bundleSteps(bundleWithDefaultServiceAccount, namespace, "", catalog)),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v1", "", "a", "alpha", catalog),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stopc := make(chan struct{})
			defer func() {
				stopc <- struct{}{}
			}()
			expectedSteps := []*v1alpha1.Step{}
			for _, steps := range tt.out.steps {
				expectedSteps = append(expectedSteps, steps...)
			}
			clientFake, informerFactory, _ := StartResolverInformers(namespace, stopc, tt.clusterState...)
			lister := operatorlister.NewLister()
			lister.OperatorsV1alpha1().RegisterSubscriptionLister(namespace, informerFactory.Operators().V1alpha1().Subscriptions().Lister())
			lister.OperatorsV1alpha1().RegisterClusterServiceVersionLister(namespace, informerFactory.Operators().V1alpha1().ClusterServiceVersions().Lister())
			lister.OperatorsV1().RegisterOperatorGroupLister(namespace, informerFactory.Operators().V1().OperatorGroups().Lister())

			stubSnapshot := &resolvercache.Snapshot{}
			for _, bundle := range tt.bundlesInCatalog {
				op, err := newOperatorFromBundle(bundle, "", catalog, "")
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				stubSnapshot.Entries = append(stubSnapshot.Entries, op)
			}
			satresolver := &Resolver{
				cache: resolvercache.New(resolvercache.StaticSourceProvider{
					catalog: stubSnapshot,
				}),
			}
			resolver := NewOperatorStepResolver(lister, clientFake, "", nil, logrus.New())
			resolver.resolver = satresolver
			steps, _, subs, err := resolver.ResolveSteps(namespace)
			require.Equal(t, tt.out.err, err)
			requireStepsEqual(t, expectedSteps, steps)
			require.ElementsMatch(t, tt.out.subs, subs)
		})
	}
}

// Helpers for resolver tests

func StartResolverInformers(namespace string, stopCh <-chan struct{}, objs ...runtime.Object) (versioned.Interface, externalversions.SharedInformerFactory, []cache.InformerSynced) {
	// Create client fakes
	clientFake := fake.NewSimpleClientset(objs...)

	var hasSyncedCheckFns []cache.InformerSynced
	nsInformerFactory := externalversions.NewSharedInformerFactoryWithOptions(clientFake, time.Second, externalversions.WithNamespace(namespace))
	informers := []cache.SharedIndexInformer{
		nsInformerFactory.Operators().V1alpha1().Subscriptions().Informer(),
		nsInformerFactory.Operators().V1alpha1().ClusterServiceVersions().Informer(),
		nsInformerFactory.Operators().V1().OperatorGroups().Informer(),
	}

	for _, informer := range informers {
		hasSyncedCheckFns = append(hasSyncedCheckFns, informer.HasSynced)
		go informer.Run(stopCh)
	}
	if ok := cache.WaitForCacheSync(stopCh, hasSyncedCheckFns...); !ok {
		panic("failed to wait for caches to sync")
	}

	return clientFake, nsInformerFactory, hasSyncedCheckFns
}

type subOption func(*v1alpha1.Subscription)

func withStartingCSV(name string) subOption {
	return func(s *v1alpha1.Subscription) {
		s.Spec.StartingCSV = name
	}
}

func newSub(namespace, pkg, channel string, catalog resolvercache.SourceKey, option ...subOption) *v1alpha1.Subscription {
	s := &v1alpha1.Subscription{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pkg + "-" + channel,
			Namespace: namespace,
		},
		Spec: &v1alpha1.SubscriptionSpec{
			Package:                pkg,
			Channel:                channel,
			CatalogSource:          catalog.Name,
			CatalogSourceNamespace: catalog.Namespace,
		},
	}
	for _, o := range option {
		o(s)
	}
	return s
}

type ogOption func(*operatorsv1.OperatorGroup)

func withUpgradeStrategy(upgradeStrategy operatorsv1.UpgradeStrategy) ogOption {
	return func(og *operatorsv1.OperatorGroup) {
		og.Spec.UpgradeStrategy = upgradeStrategy
	}
}

func newOperatorGroup(name, namespace string, option ...ogOption) *operatorsv1.OperatorGroup {
	og := &operatorsv1.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	for _, o := range option {
		o(og)
	}
	return og
}

func updatedSub(namespace, currentOperatorName, installedOperatorName, pkg, channel string, catalog resolvercache.SourceKey, option ...subOption) *v1alpha1.Subscription {
	s := &v1alpha1.Subscription{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pkg + "-" + channel,
			Namespace: namespace,
		},
		Spec: &v1alpha1.SubscriptionSpec{
			Package:                pkg,
			Channel:                channel,
			CatalogSource:          catalog.Name,
			CatalogSourceNamespace: catalog.Namespace,
		},
		Status: v1alpha1.SubscriptionStatus{
			CurrentCSV:   currentOperatorName,
			InstalledCSV: installedOperatorName,
		},
	}
	for _, o := range option {
		o(s)
	}
	return s
}

func existingSub(namespace, operatorName, pkg, channel string, catalog resolvercache.SourceKey) *v1alpha1.Subscription {
	return &v1alpha1.Subscription{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pkg + "-" + channel,
			Namespace: namespace,
		},
		Spec: &v1alpha1.SubscriptionSpec{
			Package:                pkg,
			Channel:                channel,
			CatalogSource:          catalog.Name,
			CatalogSourceNamespace: catalog.Namespace,
		},
		Status: v1alpha1.SubscriptionStatus{
			CurrentCSV:   operatorName,
			InstalledCSV: operatorName,
		},
	}
}

type csvOption func(*v1alpha1.ClusterServiceVersion)

func withPhase(phase v1alpha1.ClusterServiceVersionPhase) csvOption {
	return func(csv *v1alpha1.ClusterServiceVersion) {
		csv.Status.Phase = phase
	}
}

func existingOperator(namespace, operatorName, pkg, channel, replaces string, providedCRDs, requiredCRDs, providedAPIs, requiredAPIs resolvercache.APISet, option ...csvOption) *v1alpha1.ClusterServiceVersion {
	bundleForOperator := bundle(operatorName, pkg, channel, replaces, providedCRDs, requiredCRDs, providedAPIs, requiredAPIs)
	csv, err := V1alpha1CSVFromBundle(bundleForOperator)
	if err != nil {
		panic(err)
	}
	csv.SetNamespace(namespace)
	for _, o := range option {
		o(csv)
	}
	return csv
}

func bundleSteps(bundle *api.Bundle, ns, replaces string, catalog resolvercache.SourceKey) []*v1alpha1.Step {
	if replaces == "" {
		csv, _ := V1alpha1CSVFromBundle(bundle)
		replaces = csv.Spec.Replaces
	}
	stepresources, err := NewStepResourceFromBundle(bundle, ns, replaces, catalog.Name, catalog.Namespace)
	if err != nil {
		panic(err)
	}

	steps := make([]*v1alpha1.Step, 0)
	for _, sr := range stepresources {
		steps = append(steps, &v1alpha1.Step{
			Resolving: bundle.CsvName,
			Resource:  sr,
			Status:    v1alpha1.StepStatusUnknown,
		})
	}
	return steps
}

func withoutResourceKind(kind string, steps []*v1alpha1.Step) []*v1alpha1.Step {
	filtered := make([]*v1alpha1.Step, 0)

	for i, s := range steps {
		if s.Resource.Kind != kind {
			filtered = append(filtered, steps[i])
		}
	}

	return filtered
}

func subSteps(namespace, operatorName, pkgName, channelName string, catalog resolvercache.SourceKey) []*v1alpha1.Step {
	sub := &v1alpha1.Subscription{
		ObjectMeta: metav1.ObjectMeta{
			Name:      strings.Join([]string{pkgName, channelName, catalog.Name, catalog.Namespace}, "-"),
			Namespace: namespace,
		},
		Spec: &v1alpha1.SubscriptionSpec{
			Package:                pkgName,
			Channel:                channelName,
			CatalogSource:          catalog.Name,
			CatalogSourceNamespace: catalog.Namespace,
			StartingCSV:            operatorName,
			InstallPlanApproval:    v1alpha1.ApprovalAutomatic,
		},
	}
	stepresource, err := NewStepResourceFromObject(sub, catalog.Name, catalog.Namespace)
	if err != nil {
		panic(err)
	}
	return []*v1alpha1.Step{{
		Resolving: operatorName,
		Resource:  stepresource,
		Status:    v1alpha1.StepStatusUnknown,
	}}
}

// requireStepsEqual is similar to require.ElementsMatch, but produces better error messages
func requireStepsEqual(t *testing.T, expectedSteps, steps []*v1alpha1.Step) {
	for _, s := range expectedSteps {
		require.Contains(t, steps, s, "step in expected not found in steps")
	}
	for _, s := range steps {
		require.Contains(t, expectedSteps, s, "step in steps not found in expected")
	}
}

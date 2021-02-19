package resolver

import (
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
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-registry/pkg/api"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	controllerbundle "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/bundle"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/solver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
)

var (
	// conventions for tests: packages are letters (a,b,c) and apis are numbers (1,2,3)

	// APISets used for tests
	APISet1   = APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides1 = APISet1
	Requires1 = APISet1
	APISet2   = APISet{opregistry.APIKey{"g2", "v2", "k2", "k2s"}: struct{}{}}
	Provides2 = APISet2
	Requires2 = APISet2
	APISet3   = APISet{opregistry.APIKey{"g3", "v3", "k3", "k3s"}: struct{}{}}
	Provides3 = APISet3
	Requires3 = APISet3
	APISet4   = APISet{opregistry.APIKey{"g4", "v4", "k4", "k4s"}: struct{}{}}
	Provides4 = APISet4
	Requires4 = APISet4
)

func TestResolver(t *testing.T) {
	const namespace = "catsrc-namespace"
	catalog := registry.CatalogKey{Name: "catsrc", Namespace: namespace}

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
		querier          SourceQuerier
		bundlesByCatalog map[registry.CatalogKey][]*api.Bundle
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
			},
			out: resolverTestOut{
				solverError: solver.NotSatisfiable{
					{
						Installable: NewSubscriptionInstallable("a", nil),
						Constraint:  PrettyConstraint(solver.Mandatory(), "subscription a-alpha exists"),
					},
					{
						Installable: NewSubscriptionInstallable("a", nil),
						Constraint:  PrettyConstraint(solver.Dependency(), "no operators found matching the criteria of subscription a-alpha"),
					},
				},
			},
		},
		{
			name: "SingleNewSubscription/NoDeps",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
						Installable: NewSubscriptionInstallable("a", []solver.Identifier{"catsrc/catsrc-namespace/alpha/a.v1"}),
						Constraint:  PrettyConstraint(solver.Dependency("catsrc/catsrc-namespace/alpha/a.v1"), "subscription a-alpha requires catsrc/catsrc-namespace/alpha/a.v1"),
					},
					{
						Installable: &BundleInstallable{
							identifier:  "catsrc/catsrc-namespace/alpha/a.v1",
							constraints: []solver.Constraint{solver.Dependency()},
						},
						Constraint: solver.Dependency(),
					},
					{
						Installable: NewSubscriptionInstallable("a", []solver.Identifier{"catsrc/catsrc-namespace/alpha/a.v1"}),
						Constraint:  PrettyConstraint(solver.Mandatory(), "subscription a-alpha exists"),
					},
				}),
			},
		},
		{
			name: "InstalledSub/NoUpdates",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
					s.Name = s.Name+"-2"
					return
				}(),
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
						s.Name = s.Name+"-2"
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
			},
			out: nothing,
		},
		{
			name: "InstalledSub/UpdateAvailable",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
			name: "InstalledSub/UpdateAvailable/FromBundlePath",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{catalog: {
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{catalog: {
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", nil, nil, nil, nil),
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{catalog: {
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{catalog: {
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
			},
			bundlesByCatalog: map[registry.CatalogKey][]*api.Bundle{catalog: {
				bundle("a.v2", "a", "alpha", "", nil, nil, nil, nil, withVersion("1.0.0"), withSkips([]string{"a.v1"})),
				bundle("a.v3", "a", "alpha", "a.v2", nil, nil, nil, nil, withVersion("1.0.0"), withSkips([]string{"a.v1"})),
			}},
			out: resolverTestOut{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v3", "a", "alpha", "", nil, nil, nil, nil, withVersion("1.0.0"), withSkips([]string{"a.v1"})), namespace, "a.v1", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v3", "a.v1", "a", "alpha", catalog),
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
			kClientFake := k8sfake.NewSimpleClientset()

			stubSnapshot := &CatalogSnapshot{}
			for _, bundles := range tt.bundlesByCatalog {
				for _, bundle := range bundles {
					op, err := NewOperatorFromBundle(bundle, "", catalog, "")
					if err != nil {
						t.Fatalf("unexpected error: %v", err)
					}
					op.replaces = bundle.Replaces
					stubSnapshot.operators = append(stubSnapshot.operators, op)
				}
			}
			stubCache := &stubOperatorCacheProvider{
				noc: &NamespacedOperatorCache{
					snapshots: map[registry.CatalogKey]*CatalogSnapshot{
						catalog: stubSnapshot,
					},
				},
			}
			log := logrus.New()
			satresolver := &SatResolver{
				cache: stubCache,
				log:   log,
			}
			resolver := NewOperatorStepResolver(lister, clientFake, kClientFake, "", nil, log)
			resolver.satResolver = satresolver

			steps, lookups, subs, err := resolver.ResolveSteps(namespace, nil)
			if tt.out.solverError == nil {
				if tt.out.errAssert == nil {
					assert.Nil(t, err)
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
			RequireStepsEqual(t, expectedSteps, steps)
			require.ElementsMatch(t, tt.out.lookups, lookups)
			require.ElementsMatch(t, tt.out.subs, subs)
		})
	}
}

type stubOperatorCacheProvider struct {
	noc *NamespacedOperatorCache
}

func (stub *stubOperatorCacheProvider) Namespaced(namespaces ...string) MultiCatalogOperatorFinder {
	return stub.noc
}

func (stub *stubOperatorCacheProvider) Expire(key registry.CatalogKey) {
	return
}

func TestNamespaceResolverRBAC(t *testing.T) {
	namespace := "catsrc-namespace"
	catalog := registry.CatalogKey{"catsrc", namespace}

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
		name             string
		clusterState     []runtime.Object
		bundlesInCatalog []*api.Bundle
		out              out
	}{
		{
			name: "NewSubscription/Permissions/ClusterPermissions",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
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
			kClientFake := k8sfake.NewSimpleClientset()
			clientFake, informerFactory, _ := StartResolverInformers(namespace, stopc, tt.clusterState...)
			lister := operatorlister.NewLister()
			lister.OperatorsV1alpha1().RegisterSubscriptionLister(namespace, informerFactory.Operators().V1alpha1().Subscriptions().Lister())
			lister.OperatorsV1alpha1().RegisterClusterServiceVersionLister(namespace, informerFactory.Operators().V1alpha1().ClusterServiceVersions().Lister())

			stubSnapshot := &CatalogSnapshot{}
			for _, bundle := range tt.bundlesInCatalog {
				op, err := NewOperatorFromBundle(bundle, "", catalog, "")
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				op.replaces = bundle.Replaces
				stubSnapshot.operators = append(stubSnapshot.operators, op)
			}
			stubCache := &stubOperatorCacheProvider{
				noc: &NamespacedOperatorCache{
					snapshots: map[registry.CatalogKey]*CatalogSnapshot{
						catalog: stubSnapshot,
					},
				},
			}
			satresolver := &SatResolver{
				cache: stubCache,
			}
			resolver := NewOperatorStepResolver(lister, clientFake, kClientFake, "", nil, logrus.New())
			resolver.satResolver = satresolver
			querier := NewFakeSourceQuerier(map[registry.CatalogKey][]*api.Bundle{catalog: tt.bundlesInCatalog})
			steps, _, subs, err := resolver.ResolveSteps(namespace, querier)
			require.Equal(t, tt.out.err, err)
			RequireStepsEqual(t, expectedSteps, steps)
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

func newSub(namespace, pkg, channel string, catalog registry.CatalogKey, option ...subOption) *v1alpha1.Subscription {
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

func updatedSub(namespace, currentOperatorName, installedOperatorName, pkg, channel string, catalog registry.CatalogKey, option ...subOption) *v1alpha1.Subscription {
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

func existingSub(namespace, operatorName, pkg, channel string, catalog registry.CatalogKey) *v1alpha1.Subscription {
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

func existingOperator(namespace, operatorName, pkg, channel, replaces string, providedCRDs, requiredCRDs, providedAPIs, requiredAPIs APISet) *v1alpha1.ClusterServiceVersion {
	bundleForOperator := bundle(operatorName, pkg, channel, replaces, providedCRDs, requiredCRDs, providedAPIs, requiredAPIs)
	csv, err := V1alpha1CSVFromBundle(bundleForOperator)
	if err != nil {
		panic(err)
	}
	csv.SetNamespace(namespace)
	return csv
}

func bundleSteps(bundle *api.Bundle, ns, replaces string, catalog registry.CatalogKey) []*v1alpha1.Step {
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

func subSteps(namespace, operatorName, pkgName, channelName string, catalog registry.CatalogKey) []*v1alpha1.Step {
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

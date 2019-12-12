package resolver

import (
	"strings"
	"testing"
	"time"

	"github.com/operator-framework/operator-registry/pkg/api"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
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

func TestNamespaceResolver(t *testing.T) {
	namespace := "catsrc-namespace"
	catalog := CatalogKey{"catsrc", namespace}
	type out struct {
		steps [][]*v1alpha1.Step
		subs  []*v1alpha1.Subscription
		err   error
	}
	nothing := out{
		steps: [][]*v1alpha1.Step{},
		subs:  []*v1alpha1.Subscription{},
	}
	tests := []struct {
		name         string
		clusterState []runtime.Object
		querier      SourceQuerier
		out          out
	}{
		{
			name: "SingleNewSubscription/NoDeps",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
			},
			querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", nil, nil, nil, nil),
				},
			}),
			out: out{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v1", "a", "alpha", "", nil, nil, nil, nil), namespace, "", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v1", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "SingleNewSubscription/ResolveOne",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
			},
			querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
				catalog: {
					bundle("b.v1", "b", "beta", "", Provides1, nil, nil, nil),
					bundle("a.v1", "a", "alpha", "", nil, Requires1, nil, nil),
				},
			}),
			out: out{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v1", "a", "alpha", "", nil, Requires1, nil, nil), namespace, "", catalog),
					bundleSteps(bundle("b.v1", "b", "beta", "", Provides1, nil, nil, nil), namespace, "", catalog),
					subSteps(namespace, "b.v1", "b", "beta", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v1", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "SingleNewSubscription/ResolveOne/AdditionalBundleObjects",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
			},
			querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
				catalog: {
					withBundleObject(bundle("b.v1", "b", "beta", "", Provides1, nil, nil, nil), u(&rbacv1.RoleBinding{TypeMeta: metav1.TypeMeta{Kind: "RoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"}, ObjectMeta: metav1.ObjectMeta{Name: "test-rb"}})),
					bundle("a.v1", "a", "alpha", "", nil, Requires1, nil, nil),
				},
			}),
			out: out{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v1", "a", "alpha", "", nil, Requires1, nil, nil), namespace, "", catalog),
					bundleSteps(withBundleObject(bundle("b.v1", "b", "beta", "", Provides1, nil, nil, nil), u(&rbacv1.RoleBinding{TypeMeta: metav1.TypeMeta{Kind: "RoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"}, ObjectMeta: metav1.ObjectMeta{Name: "test-rb"}})), namespace, "", catalog),
					subSteps(namespace, "b.v1", "b", "beta", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v1", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "SingleNewSubscription/ResolveOne/AdditionalBundleObjects/Service",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
			},
			querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
				catalog: {
					withBundleObject(bundle("b.v1", "b", "beta", "", Provides1, nil, nil, nil), u(&corev1.Service{TypeMeta: metav1.TypeMeta{Kind: "Service", APIVersion: ""}, ObjectMeta: metav1.ObjectMeta{Name: "test-service"}})),
					bundle("a.v1", "a", "alpha", "", nil, Requires1, nil, nil),
				},
			}),
			out: out{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v1", "a", "alpha", "", nil, Requires1, nil, nil), namespace, "", catalog),
					bundleSteps(withBundleObject(bundle("b.v1", "b", "beta", "", Provides1, nil, nil, nil), u(&corev1.Service{TypeMeta: metav1.TypeMeta{Kind: "Service", APIVersion: ""}, ObjectMeta: metav1.ObjectMeta{Name: "test-service"}})), namespace, "", catalog),
					subSteps(namespace, "b.v1", "b", "beta", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v1", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "SingleNewSubscription/DependencyMissing",
			clusterState: []runtime.Object{
				newSub(namespace, "a", "alpha", catalog),
			},
			querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", nil, Requires1, nil, nil),
				},
			}),
			out: nothing,
		},
		{
			name: "InstalledSub/NoUpdates",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
			},
			querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
				},
			}),
			out: nothing,
		},
		{
			name: "InstalledSub/UpdateAvailable",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
			},
			querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
				catalog: {
					bundle("a.v2", "a", "alpha", "a.v1", Provides1, nil, nil, nil),
					bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
				},
			}),
			out: out{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v2", "a", "alpha", "a.v1", Provides1, nil, nil, nil), namespace, "", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v2", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "InstalledSub/NoRunningOperator",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
			},
			querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
				},
			}),
			out: out{
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
			querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", nil, nil, nil, nil),
					bundle("a.v2", "a", "alpha", "a.v1", nil, Requires1, nil, nil),
					bundle("b.v1", "b", "beta", "", Provides1, nil, nil, nil),
				},
			}),
			out: out{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v2", "a", "alpha", "a.v1", nil, Requires1, nil, nil), namespace, "", catalog),
					bundleSteps(bundle("b.v1", "b", "beta", "", Provides1, nil, nil, nil), namespace, "", catalog),
					subSteps(namespace, "b.v1", "b", "beta", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v2", "a", "alpha", catalog),
				},
			},
		},
		{
			name: "InstalledSub/UpdateFound/UpdateRequires/ResolveOne/APIServer",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", nil, nil, Provides1, nil),
			},
			querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", nil, nil, nil, nil),
					bundle("a.v2", "a", "alpha", "a.v1", nil, nil, nil, Requires1),
					bundle("b.v1", "b", "beta", "", nil, nil, Provides1, nil),
				},
			}),
			out: out{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v2", "a", "alpha", "a.v1", nil, nil, nil, Requires1), namespace, "", catalog),
					bundleSteps(bundle("b.v1", "b", "beta", "", nil, nil, Provides1, nil), namespace, "", catalog),
					subSteps(namespace, "b.v1", "b", "beta", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v2", "a", "alpha", catalog),
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
			querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", nil, nil, nil, nil),
					bundle("a.v2", "a", "alpha", "a.v1", nil, nil, nil, nil),
					bundle("b.v1", "b", "beta", "", nil, nil, nil, nil),
				},
			}),
			out: out{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v2", "a", "alpha", "a.v1", nil, nil, nil, nil), namespace, "", catalog),
					bundleSteps(bundle("b.v1", "b", "beta", "", nil, nil, nil, nil), namespace, "", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v2", "a", "alpha", catalog),
					updatedSub(namespace, "b.v1", "b", "beta", catalog),
				},
			},
		},
		{
			name: "InstalledSub/SingleNewSubscription/NoRunningOperator/ResolveOne",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				newSub(namespace, "b", "beta", catalog),
			},
			querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
					bundle("b.v1", "b", "beta", "", nil, nil, nil, nil),
				},
			}),
			out: out{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v1", "a", "alpha", "", Provides1, nil, nil, nil), namespace, "", catalog),
					bundleSteps(bundle("b.v1", "b", "beta", "", nil, nil, nil, nil), namespace, "", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "b.v1", "b", "beta", catalog),
				},
			},
		},
		{
			name: "InstalledSub/SingleNewSubscription/NoRunningOperator/ResolveOne/APIServer",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				newSub(namespace, "b", "beta", catalog),
			},
			querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
				catalog: {
					bundle("a.v1", "a", "alpha", "", nil, nil, Provides1, nil),
					bundle("b.v1", "b", "beta", "", nil, nil, nil, nil),
				},
			}),
			out: out{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v1", "a", "alpha", "", nil, nil, Provides1, nil), namespace, "", catalog),
					bundleSteps(bundle("b.v1", "b", "beta", "", nil, nil, nil, nil), namespace, "", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "b.v1", "b", "beta", catalog),
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
			querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
				catalog: {
					bundle("a.v2", "a", "alpha", "a.v1", Provides3, Requires4, nil, nil),
					bundle("b.v2", "b", "alpha", "b.v1", Provides4, Requires3, nil, nil),
				},
			}),
			out: out{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v2", "a", "alpha", "a.v1", Provides3, Requires4, nil, nil), namespace, "", catalog),
					bundleSteps(bundle("b.v2", "b", "alpha", "b.v1", Provides4, Requires3, nil, nil), namespace, "", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v2", "a", "alpha", catalog),
					updatedSub(namespace, "b.v2", "b", "alpha", catalog),
				},
			},
		},
		{
			name: "InstalledSub/UpdateInHead/SkipRange",
			clusterState: []runtime.Object{
				existingSub(namespace, "a.v1", "a", "alpha", catalog),
				existingOperator(namespace, "a.v1", "a", "alpha", "", Provides1, nil, nil, nil),
			},
			querier: NewFakeSourceQuerierCustomReplacement(catalog, bundle("a.v3", "a", "alpha", "a.v2", nil, nil, nil, nil)),
			out: out{
				steps: [][]*v1alpha1.Step{
					bundleSteps(bundle("a.v3", "a", "alpha", "a.v2", nil, nil, nil, nil), namespace, "a.v1", catalog),
				},
				subs: []*v1alpha1.Subscription{
					updatedSub(namespace, "a.v3", "a", "alpha", catalog),
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

			resolver := NewOperatorsV1alpha1Resolver(lister, clientFake)
			steps, subs, err := resolver.ResolveSteps(namespace, tt.querier)
			require.Equal(t, tt.out.err, err)
			t.Logf("%#v", steps)
			RequireStepsEqual(t, expectedSteps, steps)
			require.ElementsMatch(t, tt.out.subs, subs)
		})
	}
}

func TestNamespaceResolverRBAC(t *testing.T) {
	generateName = func(base string) string {
		return "a"
	}

	namespace := "catsrc-namespace"
	catalog := CatalogKey{"catsrc", namespace}

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
					updatedSub(namespace, "a.v1", "a", "alpha", catalog),
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

			resolver := NewOperatorsV1alpha1Resolver(lister, clientFake)
			querier := NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{catalog: tt.bundlesInCatalog})
			steps, subs, err := resolver.ResolveSteps(namespace, querier)
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

func newSub(namespace, pkg, channel string, catalog CatalogKey) *v1alpha1.Subscription {
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
	}
}

func updatedSub(namespace, operatorName, pkg, channel string, catalog CatalogKey) *v1alpha1.Subscription {
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
			CurrentCSV: operatorName,
		},
	}
}

func existingSub(namespace, operatorName, pkg, channel string, catalog CatalogKey) *v1alpha1.Subscription {
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
			CurrentCSV: operatorName,
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

func bundleSteps(bundle *api.Bundle, ns, replaces string, catalog CatalogKey) []*v1alpha1.Step {
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

func subSteps(namespace, operatorName, pkgName, channelName string, catalog CatalogKey) []*v1alpha1.Step {
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

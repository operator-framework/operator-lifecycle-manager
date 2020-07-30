package resolver

import (
	"testing"

	"github.com/stretchr/testify/require"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
)



func TestResolverLegacy(t *testing.T) {
	namespace := "catsrc-namespace"
	for _, tt := range SharedResolverSpecs() {
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

			resolver := NewLegacyResolver(lister, clientFake, kClientFake, "")

			tt.querier = NewFakeSourceQuerier(tt.bundlesByCatalog)
			steps, lookups, subs, err := resolver.ResolveSteps(namespace, tt.querier)
			require.Equal(t, tt.out.err, err)
			RequireStepsEqual(t, expectedSteps, steps)
			require.ElementsMatch(t, tt.out.lookups, lookups)
			require.ElementsMatch(t, tt.out.subs, subs)
		})
	}
}

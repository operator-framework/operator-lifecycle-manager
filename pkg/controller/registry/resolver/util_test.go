package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/blang/semver"
	"github.com/operator-framework/operator-registry/pkg/client"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/fakes"
)

// RequireStepsEqual is similar to require.ElementsMatch, but produces better error messages
func RequireStepsEqual(t *testing.T, expectedSteps, steps []*v1alpha1.Step) {
	for _, s := range expectedSteps {
		require.Contains(t, steps, s)
	}
	for _, s := range steps {
		require.Contains(t, expectedSteps, s)
	}
}

func NewGenerationFromOperators(ops ...OperatorSurface) *NamespaceGeneration {
	g := NewEmptyGeneration()

	for _, op := range ops {
		if err := g.AddOperator(op); err != nil {
			fmt.Printf("error adding operator: %s\n", err.Error())
			return nil
		}
	}
	return g
}

func NewFakeOperatorSurface(name, pkg, channel, replaces, src, startingCSV string, providedCRDs, requiredCRDs, providedAPIServices, requiredAPIServices []opregistry.APIKey) *Operator {
	providedAPISet := EmptyAPISet()
	requiredAPISet := EmptyAPISet()
	providedCRDAPISet := EmptyAPISet()
	requiredCRDAPISet := EmptyAPISet()
	providedAPIServiceAPISet := EmptyAPISet()
	requiredAPIServiceAPISet := EmptyAPISet()
	version := semver.MustParse("0.0.0")

	for _, p := range providedCRDs {
		providedCRDAPISet[p] = struct{}{}
		providedAPISet[p] = struct{}{}
	}
	for _, r := range requiredCRDs {
		requiredCRDAPISet[r] = struct{}{}
		requiredAPISet[r] = struct{}{}
	}
	for _, p := range providedAPIServices {
		providedAPIServiceAPISet[p] = struct{}{}
		providedAPISet[p] = struct{}{}
	}
	for _, r := range requiredAPIServices {
		requiredAPIServiceAPISet[r] = struct{}{}
		requiredAPISet[r] = struct{}{}
	}
	b := bundle(name, pkg, channel, replaces, providedCRDAPISet, requiredCRDAPISet, providedAPIServiceAPISet, requiredAPIServiceAPISet)
	// force bundle cache to fill
	_, _ = b.ClusterServiceVersion()
	_, _ = b.CustomResourceDefinitions()

	return &Operator{
		providedAPIs: providedAPISet,
		requiredAPIs: requiredAPISet,
		name:         name,
		replaces:     replaces,
		version:      &version,
		sourceInfo: &OperatorSourceInfo{
			Package:     pkg,
			Channel:     channel,
			StartingCSV: startingCSV,
			Catalog:     CatalogKey{src, src + "-namespace"},
		},
		bundle: b,
	}
}

func csv(name, replaces string, ownedCRDs, requiredCRDs, ownedAPIServices, requiredAPIServices APISet, permissions, clusterPermissions []install.StrategyDeploymentPermissions) *v1alpha1.ClusterServiceVersion {
	var singleInstance = int32(1)
	strategy := install.StrategyDetailsDeployment{
		Permissions:        permissions,
		ClusterPermissions: clusterPermissions,
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: name,
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": name,
						},
					},
					Replicas: &singleInstance,
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": name,
							},
						},
						Spec: corev1.PodSpec{
							ServiceAccountName: "sa",
							Containers: []corev1.Container{
								{
									Name:  name + "-c1",
									Image: "nginx:1.7.9",
									Ports: []corev1.ContainerPort{
										{
											ContainerPort: 80,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	strategyRaw, err := json.Marshal(strategy)
	if err != nil {
		panic(err)
	}

	installStrategy := v1alpha1.NamedInstallStrategy{
		StrategyName:    install.InstallStrategyNameDeployment,
		StrategySpecRaw: strategyRaw,
	}

	requiredCRDDescs := make([]v1alpha1.CRDDescription, 0)
	for crd := range requiredCRDs {
		requiredCRDDescs = append(requiredCRDDescs, v1alpha1.CRDDescription{Name: crd.Plural + "." + crd.Group, Version: crd.Version, Kind: crd.Kind})
	}

	ownedCRDDescs := make([]v1alpha1.CRDDescription, 0)
	for crd := range ownedCRDs {
		ownedCRDDescs = append(ownedCRDDescs, v1alpha1.CRDDescription{Name: crd.Plural + "." + crd.Group, Version: crd.Version, Kind: crd.Kind})
	}

	requiredAPIDescs := make([]v1alpha1.APIServiceDescription, 0)
	for api := range requiredAPIServices {
		requiredAPIDescs = append(requiredAPIDescs, v1alpha1.APIServiceDescription{Name: api.Plural, Group: api.Group, Version: api.Version, Kind: api.Kind})
	}

	ownedAPIDescs := make([]v1alpha1.APIServiceDescription, 0)
	for api := range ownedAPIServices {
		ownedAPIDescs = append(ownedAPIDescs, v1alpha1.APIServiceDescription{Name: api.Plural, Group: api.Group, Version: api.Version, Kind: api.Kind, DeploymentName: name, ContainerPort: 80})
	}

	return &v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces:        replaces,
			InstallStrategy: installStrategy,
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned:    ownedCRDDescs,
				Required: requiredCRDDescs,
			},
			APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
				Owned:    ownedAPIDescs,
				Required: requiredAPIDescs,
			},
		},
	}
}

func crd(key opregistry.APIKey) *v1beta1.CustomResourceDefinition {
	return &v1beta1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CustomResourceDefinition",
			APIVersion: v1beta1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: key.Plural + "." + key.Group,
		},
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group: key.Group,
			Versions: []v1beta1.CustomResourceDefinitionVersion{
				{
					Name:    key.Version,
					Storage: true,
					Served:  true,
				},
			},
			Names: v1beta1.CustomResourceDefinitionNames{
				Kind:   key.Kind,
				Plural: key.Plural,
			},
		},
	}
}

func u(object runtime.Object) *unstructured.Unstructured {
	fmt.Println(object)
	unst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(object)
	if err != nil {
		panic(err)
	}
	return &unstructured.Unstructured{Object: unst}
}

func bundle(name, pkg, channel, replaces string, providedCRDs, requiredCRDs, providedAPIServices, requiredAPIServices APISet) *opregistry.Bundle {
	bundleObjs := []*unstructured.Unstructured{u(csv(name, replaces, providedCRDs, requiredCRDs, providedAPIServices, requiredAPIServices, nil, nil))}
	for p := range providedCRDs {
		bundleObjs = append(bundleObjs, u(crd(p)))
	}
	return opregistry.NewBundle(name, pkg, channel, bundleObjs...)
}

func withBundleObject(bundle *opregistry.Bundle, obj *unstructured.Unstructured) *opregistry.Bundle {
	bundle.Add(obj)
	return bundle
}

func bundleWithPermissions(name, pkg, channel, replaces string, providedCRDs, requiredCRDs, providedAPIServices, requiredAPIServices APISet, permissions, clusterPermissions []install.StrategyDeploymentPermissions) *opregistry.Bundle {
	bundleObjs := []*unstructured.Unstructured{u(csv(name, replaces, providedCRDs, requiredCRDs, providedAPIServices, requiredAPIServices, permissions, clusterPermissions))}
	for p := range providedCRDs {
		bundleObjs = append(bundleObjs, u(crd(p)))
	}
	return opregistry.NewBundle(name, pkg, channel, bundleObjs...)
}

// TestBundle verifies that the bundle stubbing works as expected
func TestBundleStub(t *testing.T) {
	tests := []struct {
		name             string
		bundle           *opregistry.Bundle
		wantProvidedAPIs APISet
		wantRequiredAPIs APISet
	}{
		{
			name:   "RequiredCRDs",
			bundle: bundle("depender.v1", "depender", "channel", "", EmptyAPISet(), APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}}, EmptyAPISet(), EmptyAPISet()),
			wantRequiredAPIs: APISet{
				opregistry.APIKey{"g", "v", "k", "ks"}: {},
			},
		},
		{
			name:   "ProvidedCRDs",
			bundle: bundle("provider.v1", "provider", "channel", "", APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}}, EmptyAPISet(), EmptyAPISet(), EmptyAPISet()),
			wantProvidedAPIs: APISet{
				opregistry.APIKey{"g", "v", "k", "ks"}: {},
			},
		},
		{
			name:   "RequiredAPIs",
			bundle: bundle("depender.v1", "depender", "channel", "", EmptyAPISet(), EmptyAPISet(), EmptyAPISet(), APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}}),
			wantRequiredAPIs: APISet{
				opregistry.APIKey{"g", "v", "k", "ks"}: {},
			},
		},
		{
			name:   "ProvidedAPIs",
			bundle: bundle("provider.v1", "provider", "channel", "", EmptyAPISet(), EmptyAPISet(), APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}}, EmptyAPISet()),
			wantProvidedAPIs: APISet{
				opregistry.APIKey{"g", "v", "k", "ks"}: {},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantProvidedAPIs != nil {
				provided, err := tt.bundle.ProvidedAPIs()
				require.NoError(t, err)
				require.EqualValues(t, tt.wantProvidedAPIs, provided)
			}
			if tt.wantRequiredAPIs != nil {
				required, err := tt.bundle.RequiredAPIs()
				require.NoError(t, err)
				require.EqualValues(t, tt.wantRequiredAPIs, required)
			}
		})
	}

}

// NewFakeSourceQuerier builds a querier that talks to fake registry stubs for testing
func NewFakeSourceQuerier(bundlesByCatalog map[CatalogKey][]*opregistry.Bundle) *NamespaceSourceQuerier {
	sources := map[CatalogKey]client.Interface{}
	for catKey, bundles := range bundlesByCatalog {
		source := &fakes.FakeInterface{}
		source.GetBundleThatProvidesStub = func(ctx context.Context, groupOrName, version, kind string) (*opregistry.Bundle, error) {
			for _, b := range bundles {
				apis, err := b.ProvidedAPIs()
				if err != nil {
					return nil, err
				}
				for api := range apis {
					if api.Version == version && api.Kind == kind && strings.Contains(groupOrName, api.Group) && strings.Contains(groupOrName, api.Plural) {
						return b, nil
					}
				}
			}
			return nil, fmt.Errorf("no bundle found")
		}
		// note: this only allows for one bundle per package/channel, which may be enough for tests
		source.GetBundleInPackageChannelStub = func(ctx context.Context, packageName, channelName string) (*opregistry.Bundle, error) {
			for _, b := range bundles {
				if b.Channel == channelName && b.Package == packageName {
					return b, nil
				}
			}
			return nil, fmt.Errorf("no bundle found")
		}

		source.GetBundleStub = func(ctx context.Context, packageName, channelName, csvName string) (*opregistry.Bundle, error) {
			for _, b := range bundles {
				if b.Channel == channelName && b.Package == packageName && b.Name == csvName {
					return b, nil
				}
			}
			return nil, fmt.Errorf("no bundle found")
		}

		source.GetReplacementBundleInPackageChannelStub = func(ctx context.Context, bundleName, packageName, channelName string) (*opregistry.Bundle, error) {
			for _, b := range bundles {
				csv, err := b.ClusterServiceVersion()
				if err != nil {
					panic(err)
				}
				if csv.Spec.Replaces == bundleName && b.Channel == channelName && b.Package == packageName {
					return b, nil
				}
			}
			return nil, fmt.Errorf("no bundle found")
		}
		sources[catKey] = source
	}
	return NewNamespaceSourceQuerier(sources)
}

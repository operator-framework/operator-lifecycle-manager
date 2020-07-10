package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/blang/semver"
	"github.com/operator-framework/operator-registry/pkg/api"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/fakes"
)

// RequireStepsEqual is similar to require.ElementsMatch, but produces better error messages
func RequireStepsEqual(t *testing.T, expectedSteps, steps []*v1alpha1.Step) {
	for _, s := range expectedSteps {
		require.Contains(t, steps, s, "step in expected not found in steps")
	}
	for _, s := range steps {
		require.Contains(t, expectedSteps, s, "step in steps not found in expected")
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

func NewFakeOperatorSurface(name, pkg, channel, replaces, src, startingCSV string, providedCRDs, requiredCRDs, providedAPIServices, requiredAPIServices []opregistry.APIKey, dependencies []VersionDependency) *Operator {
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
		bundle:              b,
		versionDependencies: dependencies,
	}
}

func csv(name, replaces string, ownedCRDs, requiredCRDs, ownedAPIServices, requiredAPIServices APISet, permissions, clusterPermissions []v1alpha1.StrategyDeploymentPermissions) *v1alpha1.ClusterServiceVersion {
	var singleInstance = int32(1)
	strategy := v1alpha1.StrategyDetailsDeployment{
		Permissions:        permissions,
		ClusterPermissions: clusterPermissions,
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
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

	installStrategy := v1alpha1.NamedInstallStrategy{
		StrategyName: v1alpha1.InstallStrategyNameDeployment,
		StrategySpec: strategy,
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
	unst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(object)
	if err != nil {
		panic(err)
	}
	return &unstructured.Unstructured{Object: unst}
}

func apiSetToGVk(crds, apis APISet) (out []*api.GroupVersionKind) {
	out = make([]*api.GroupVersionKind, 0)
	for a := range crds {
		out = append(out, &api.GroupVersionKind{
			Group:   a.Group,
			Version: a.Version,
			Kind:    a.Kind,
			Plural:  a.Plural,
		})
	}
	for a := range apis {
		out = append(out, &api.GroupVersionKind{
			Group:   a.Group,
			Version: a.Version,
			Kind:    a.Kind,
			Plural:  a.Plural,
		})
	}
	return
}

func bundle(name, pkg, channel, replaces string, providedCRDs, requiredCRDs, providedAPIServices, requiredAPIServices APISet) *api.Bundle {
	csvJson, err := json.Marshal(csv(name, replaces, providedCRDs, requiredCRDs, providedAPIServices, requiredAPIServices, nil, nil))
	if err != nil {
		panic(err)
	}

	objs := []string{string(csvJson)}
	for p := range providedCRDs {
		crdJson, err := json.Marshal(crd(p))
		if err != nil {
			panic(err)
		}
		objs = append(objs, string(crdJson))
	}

	return &api.Bundle{
		CsvName:      name,
		PackageName:  pkg,
		ChannelName:  channel,
		CsvJson:      string(csvJson),
		Object:       objs,
		Version:      "0.0.0",
		ProvidedApis: apiSetToGVk(providedCRDs, providedAPIServices),
		RequiredApis: apiSetToGVk(requiredCRDs, requiredAPIServices),
	}
}

func stripManifests(bundle *api.Bundle) *api.Bundle {
	bundle.CsvJson = ""
	bundle.Object = nil
	return bundle
}

func withBundleObject(bundle *api.Bundle, obj *unstructured.Unstructured) *api.Bundle {
	j, err := json.Marshal(obj)
	if err != nil {
		panic(err)
	}
	bundle.Object = append(bundle.Object, string(j))
	return bundle
}

func withBundlePath(bundle *api.Bundle, path string) *api.Bundle {
	bundle.BundlePath = path
	return bundle
}

func bundleWithPermissions(name, pkg, channel, replaces string, providedCRDs, requiredCRDs, providedAPIServices, requiredAPIServices APISet, permissions, clusterPermissions []v1alpha1.StrategyDeploymentPermissions) *api.Bundle {
	csvJson, err := json.Marshal(csv(name, replaces, providedCRDs, requiredCRDs, providedAPIServices, requiredAPIServices, permissions, clusterPermissions))
	if err != nil {
		panic(err)
	}

	objs := []string{string(csvJson)}
	for p := range providedCRDs {
		crdJson, err := json.Marshal(crd(p))
		if err != nil {
			panic(err)
		}
		objs = append(objs, string(crdJson))
	}

	return &api.Bundle{
		CsvName:      name,
		PackageName:  pkg,
		ChannelName:  channel,
		CsvJson:      string(csvJson),
		Object:       objs,
		ProvidedApis: apiSetToGVk(providedCRDs, providedAPIServices),
		RequiredApis: apiSetToGVk(requiredCRDs, requiredAPIServices),
	}
}

func withReplaces(operator *Operator, replaces string) *Operator {
	operator.replaces = replaces
	return operator
}

// NewFakeSourceQuerier builds a querier that talks to fake registry stubs for testing
func NewFakeSourceQuerier(bundlesByCatalog map[CatalogKey][]*api.Bundle) *NamespaceSourceQuerier {
	sources := map[CatalogKey]registry.ClientInterface{}
	for catKey, bundles := range bundlesByCatalog {
		source := &fakes.FakeClientInterface{}
		source.GetBundleThatProvidesStub = func(ctx context.Context, groupOrName, version, kind string) (*api.Bundle, error) {
			for _, b := range bundles {
				apis := b.GetProvidedApis()
				for _, api := range apis {
					if api.Version == version && api.Kind == kind && strings.Contains(groupOrName, api.Group) && strings.Contains(groupOrName, api.Plural) {
						return b, nil
					}
				}
			}
			return nil, fmt.Errorf("no bundle found")
		}
		// note: this only allows for one bundle per package/channel, which may be enough for tests
		source.GetBundleInPackageChannelStub = func(ctx context.Context, packageName, channelName string) (*api.Bundle, error) {
			for _, b := range bundles {
				if b.ChannelName == channelName && b.PackageName == packageName {
					return b, nil
				}
			}
			return nil, fmt.Errorf("no bundle found")
		}

		source.GetBundleStub = func(ctx context.Context, packageName, channelName, csvName string) (*api.Bundle, error) {
			for _, b := range bundles {
				if b.ChannelName == channelName && b.PackageName == packageName && b.CsvName == csvName {
					return b, nil
				}
			}
			return nil, fmt.Errorf("no bundle found")
		}

		source.GetReplacementBundleInPackageChannelStub = func(ctx context.Context, bundleName, packageName, channelName string) (*api.Bundle, error) {
			for _, b := range bundles {
				if len(b.CsvJson) == 0 {
					continue
				}
				csv, err := V1alpha1CSVFromBundle(b)
				if err != nil {
					panic(err)
				}
				replaces := csv.Spec.Replaces
				if replaces == bundleName && b.ChannelName == channelName && b.PackageName == packageName {
					return b, nil
				}
			}

			return nil, fmt.Errorf("no bundle found")
		}

		source.FindBundleThatProvidesStub = func(ctx context.Context, groupOrName, version, kind string, excludedPkgs map[string]struct{}) (*api.Bundle, error) {
			bundles, ok := bundlesByCatalog[catKey]
			if !ok {
				return nil, fmt.Errorf("API (%s/%s/%s) not provided by a package in %s CatalogSource", groupOrName, version, kind, catKey)
			}
			sortedBundles := SortBundleInPackageChannel(bundles)
			for k, v := range sortedBundles {
				pkgname := getPkgName(k)
				if _, ok := excludedPkgs[pkgname]; ok {
					continue
				}

				for i := len(v) - 1; i >= 0; i-- {
					b := v[i]
					apis := b.GetProvidedApis()
					for _, api := range apis {
						if api.Version == version && api.Kind == kind && strings.Contains(groupOrName, api.Group) && strings.Contains(groupOrName, api.Plural) {
							return b, nil
						}
					}
				}
			}
			return nil, fmt.Errorf("no bundle found")
		}

		sources[catKey] = source
	}
	return NewNamespaceSourceQuerier(sources)
}

// SortBundleInPackageChannel will sort into map of package-channel key and list
// sorted (oldest to latest version) of bundles as value
func SortBundleInPackageChannel(bundles []*api.Bundle) map[string][]*api.Bundle {
	sorted := map[string][]*api.Bundle{}
	var initialReplaces string
	for _, v := range bundles {
		pkgChanKey := v.PackageName + "/" + v.ChannelName
		sorted[pkgChanKey] = append(sorted[pkgChanKey], v)
	}

	for k, v := range sorted {
		resorted := []*api.Bundle{}
		bundleMap := map[string]*api.Bundle{}
		// Find the first (oldest) bundle in upgrade graph
		for _, bundle := range v {
			csv, err := V1alpha1CSVFromBundle(bundle)
			if err != nil {
				initialReplaces = bundle.CsvName
				resorted = append(resorted, bundle)
				continue
			}

			if replaces := csv.Spec.Replaces; replaces == "" {
				initialReplaces = bundle.CsvName
				resorted = append(resorted, bundle)
			} else {
				bundleMap[replaces] = bundle
			}
		}
		resorted = sortBundleInChannel(initialReplaces, bundleMap, resorted)
		sorted[k] = resorted
	}
	return sorted
}

// sortBundleInChannel recursively sorts a list of bundles to form a update graph
// of a specific channel. The first item in the returned list is the start
// (oldest version) of the upgrade graph and the last item is the head
// (latest version) of a channel
func sortBundleInChannel(replaces string, replacedBundle map[string]*api.Bundle, updated []*api.Bundle) []*api.Bundle {
	bundle, ok := replacedBundle[replaces]
	if ok {
		updated = append(updated, bundle)
		return sortBundleInChannel(bundle.CsvName, replacedBundle, updated)
	}
	return updated
}

// getPkgName splits the package/channel string and return package name
func getPkgName(pkgChan string) string {
	s := strings.Split(pkgChan, "/")
	return s[0]
}

// NewFakeSourceQuerier builds a querier that talks to fake registry stubs for testing
func NewFakeSourceQuerierCustomReplacement(catKey CatalogKey, bundle *api.Bundle) *NamespaceSourceQuerier {
	sources := map[CatalogKey]registry.ClientInterface{}
	source := &fakes.FakeClientInterface{}
	source.GetBundleThatProvidesStub = func(ctx context.Context, groupOrName, version, kind string) (*api.Bundle, error) {
		return nil, fmt.Errorf("no bundle found")
	}
	source.GetBundleInPackageChannelStub = func(ctx context.Context, packageName, channelName string) (*api.Bundle, error) {
		return nil, fmt.Errorf("no bundle found")
	}
	source.GetBundleStub = func(ctx context.Context, packageName, channelName, csvName string) (*api.Bundle, error) {
		return nil, fmt.Errorf("no bundle found")
	}
	source.GetReplacementBundleInPackageChannelStub = func(ctx context.Context, bundleName, packageName, channelName string) (*api.Bundle, error) {
		return bundle, nil
	}
	sources[catKey] = source
	return NewNamespaceSourceQuerier(sources)
}

package resolver

import (
	"errors"
	"testing"

	olmerrors "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/errors"
	log "github.com/sirupsen/logrus"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/stretchr/testify/require"
)

const (
	crdKind = "CustomResourceDefinition"
	csvKind = v1alpha1.ClusterServiceVersionKind
)

func resolveInstallPlan(t *testing.T, resolver DependencyResolver) {
	type csvNames struct {
		name     string
		owned    []string
		required []string
	}
	var table = []struct {
		description     string
		planCSVName     string
		csv             []csvNames
		crdNames        []string
		expectedErr     error
		expectedPlanLen int
	}{
		{"MissingCSV", "name", []csvNames{{"", nil, nil}}, nil, errors.New("not found: ClusterServiceVersion name"), 0},
		{"MissingCSVByName", "name", []csvNames{{"missingName", nil, nil}}, nil, errors.New("not found: ClusterServiceVersion name"), 0},
		{"FoundCSV", "name", []csvNames{{"name", nil, nil}}, nil, nil, 1},
		{"CSVWithMissingOwnedCRD", "name", []csvNames{{"name", []string{"missingCRD"}, nil}}, nil, errors.New("not found: CRD missingCRD/missingCRD/v1"), 0},
		{"CSVWithMissingRequiredCRD", "name", []csvNames{{"name", nil, []string{"missingCRD"}}}, nil, errors.New("not found: CRD missingCRD/missingCRD/v1"), 0},
		{"FoundCSVWithCRD", "name", []csvNames{{"name", []string{"CRD"}, nil}}, []string{"CRD"}, nil, 4},
		{"FoundCSVWithDependency", "name", []csvNames{{"name", nil, []string{"CRD"}}, {"crdOwner", []string{"CRD"}, nil}}, []string{"CRD"}, nil, 5},
	}

	for _, tt := range table {
		t.Run(tt.description, func(t *testing.T) {
			namespace := "default"

			log.SetLevel(log.DebugLevel)
			// Create a plan that is attempting to install the planCSVName.
			plan := installPlan(namespace, tt.planCSVName)

			// Create a catalog source containing a CSVs and CRDs with the provided
			// names.
			src := registry.NewInMem()
			for _, name := range tt.crdNames {
				err := src.SetCRDDefinition(crd(name, namespace))
				require.NoError(t, err)
			}
			for _, names := range tt.csv {
				// We add unsafe so that we can test invalid states
				src.AddOrReplaceService(csv(names.name, namespace, names.owned, names.required))
			}

			srcKey := registry.ResourceKey{
				Name:      "ocs",
				Namespace: plan.Namespace,
			}

			srcRef := registry.SourceRef{
				Source:    src,
				SourceKey: srcKey,
			}
			// Generate an ordered list of source refs
			srcRefs := []registry.SourceRef{srcRef}

			// No existing CSVs in the install plan namespace
			existingCSVNames := make(map[string][]string)

			// Resolve the plan
			steps, _, err := resolver.ResolveInstallPlan(srcRefs, existingCSVNames, "alm-catalog", &plan)
			plan.Status.Plan = steps

			// Assert the error is as expected
			if tt.expectedErr == nil {
				require.Nil(t, err)
			} else {
				require.Equal(t, tt.expectedErr, err)
			}

			// Assert the number of items in the plan are equal
			require.Equal(t, tt.expectedPlanLen, len(plan.Status.Plan))

			// Assert that all StepResources have the have the correct catalog source name and namespace set
			for _, step := range plan.Status.Plan {
				require.Equal(t, step.Resource.CatalogSource, "ocs")
				require.Equal(t, step.Resource.CatalogSourceNamespace, plan.Namespace)
			}
		})
	}
}

func multiSourceResolveInstallPlan(t *testing.T, resolver DependencyResolver) {

	// Define some source keys representing different catalog sources (all in same namespace for now)
	sourceA := registry.ResourceKey{Namespace: "default", Name: "ocs-a"}
	sourceB := registry.ResourceKey{Namespace: "default", Name: "ocs-b"}
	sourceC := registry.ResourceKey{Namespace: "default", Name: "ocs-c"}

	type resourceKey struct {
		name string
		kind string
	}
	type csvName struct {
		name     string
		owned    []string
		required []string
		srcKey   registry.ResourceKey
	}
	type crdName struct {
		name   string
		srcKey registry.ResourceKey
	}
	var table = []struct {
		description       string
		csvs              []csvName
		crds              []crdName
		srcKeys           []registry.ResourceKey
		expectedErr       error
		expectedResources map[resourceKey]registry.ResourceKey
	}{
		{
			"SingleCRDSameCatalog",
			[]csvName{
				{"main", nil, []string{"CRD"}, sourceA},
				{"crdOwner", []string{"CRD"}, nil, sourceA},
			},
			[]crdName{{"CRD", sourceA}},
			[]registry.ResourceKey{sourceA},
			nil,
			map[resourceKey]registry.ResourceKey{
				resourceKey{"main", csvKind}:              sourceA,
				resourceKey{"crdOwner", csvKind}:          sourceA,
				resourceKey{"CRD", crdKind}:               sourceA,
				resourceKey{"edit-CRD-v1", "ClusterRole"}: sourceA,
				resourceKey{"view-CRD-v1", "ClusterRole"}: sourceA,
			},
		},
		{
			"SingleCRDDifferentCatalog",
			[]csvName{
				{"main", nil, []string{"CRD"}, sourceA},
				{"crdOwner", []string{"CRD"}, nil, sourceB},
			},
			[]crdName{{"CRD", sourceB}},
			[]registry.ResourceKey{sourceA, sourceB},
			nil,
			map[resourceKey]registry.ResourceKey{
				resourceKey{"main", csvKind}:              sourceA,
				resourceKey{"crdOwner", csvKind}:          sourceB,
				resourceKey{"CRD", crdKind}:               sourceB,
				resourceKey{"edit-CRD-v1", "ClusterRole"}: sourceB,
				resourceKey{"view-CRD-v1", "ClusterRole"}: sourceB,
			},
		},
		{
			"RequiredCRDNotInOwnersCatalog",
			[]csvName{
				{"main", nil, []string{"CRD"}, sourceA},
				{"crdOwner", []string{"CRD"}, nil, sourceB},
			},
			[]crdName{{"CRD", sourceC}},
			[]registry.ResourceKey{sourceA, sourceB, sourceC},
			errors.New("not found: CRD CRD/CRD/v1"),
			nil,
		},
		{
			"MultipleTransitiveDependenciesInDifferentCatalogs",
			[]csvName{
				{"main", nil, []string{"CRD-0"}, sourceA},
				{"crdOwner-0", []string{"CRD-0"}, []string{"CRD-1"}, sourceB},
				{"crdOwner-1", []string{"CRD-1", "CRD-2"}, nil, sourceC},
			},
			[]crdName{
				{"CRD-0", sourceB},
				{"CRD-1", sourceC},
				{"CRD-2", sourceC},
			},
			[]registry.ResourceKey{sourceA, sourceB, sourceC},
			nil,
			map[resourceKey]registry.ResourceKey{
				resourceKey{"main", csvKind}:                sourceA,
				resourceKey{"crdOwner-0", csvKind}:          sourceB,
				resourceKey{"crdOwner-1", csvKind}:          sourceC,
				resourceKey{"CRD-0", crdKind}:               sourceB,
				resourceKey{"edit-CRD-0-v1", "ClusterRole"}: sourceB,
				resourceKey{"view-CRD-0-v1", "ClusterRole"}: sourceB,
				resourceKey{"CRD-1", crdKind}:               sourceC,
				resourceKey{"edit-CRD-1-v1", "ClusterRole"}: sourceC,
				resourceKey{"view-CRD-1-v1", "ClusterRole"}: sourceC,
				resourceKey{"CRD-2", crdKind}:               sourceC,
				resourceKey{"edit-CRD-2-v1", "ClusterRole"}: sourceC,
				resourceKey{"view-CRD-2-v1", "ClusterRole"}: sourceC,
			},
		},
	}

	for _, tt := range table {
		t.Run(tt.description, func(t *testing.T) {
			log.SetLevel(log.DebugLevel)
			// Create a plan that is attempting to install the planCSVName.
			plan := installPlan("default", "main")

			// Create catalog sources for all given srcKeys
			sources := map[registry.ResourceKey]*registry.InMem{}
			for _, srcKey := range tt.srcKeys {
				src := registry.NewInMem()
				sources[srcKey] = src
			}

			// Add CRDs and CSVs to the approprate sources
			for _, name := range tt.crds {
				source := sources[name.srcKey]
				err := source.SetCRDDefinition(crd(name.name, name.srcKey.Namespace))
				require.NoError(t, err)
			}
			for _, name := range tt.csvs {
				// We add unsafe so that we can test invalid states
				source := sources[name.srcKey]
				source.AddOrReplaceService(csv(name.name, name.srcKey.Namespace, name.owned, name.required))
			}

			// Generate an ordered list of source refs
			srcRefs := make([]registry.SourceRef, len(sources))
			i := 0
			for srcKey, source := range sources {
				srcRefs[i] = registry.SourceRef{
					Source:    source,
					SourceKey: srcKey,
				}
				i++
			}

			// No existing CSVs in the install plan namespace
			existingCSVNames := make(map[string][]string)

			// Resolve the plan.
			steps, _, err := resolver.ResolveInstallPlan(srcRefs, existingCSVNames, "alm-catalog", &plan)

			// Set the plan and used Sources
			plan.Status.Plan = steps

			// Assert the error is as expected
			if tt.expectedErr == nil {
				require.Nil(t, err)
			} else {
				require.Equal(t, tt.expectedErr, err)
			}

			require.Equal(t, len(tt.expectedResources), len(plan.Status.Plan))

			// Assert that all StepResources have the have the correct CatalogSource set
			for _, step := range plan.Status.Plan {
				resourceKey := resourceKey{step.Resource.Name, step.Resource.Kind}
				expectedSource := tt.expectedResources[resourceKey]

				require.Equal(t, expectedSource.Name, step.Resource.CatalogSource, "%v source name different", resourceKey)
				require.Equal(t, expectedSource.Namespace, step.Resource.CatalogSourceNamespace, "%v source namespace different", resourceKey)
			}
		})
	}
}

func namespaceAndChannelAwareResolveInstallPlan(t *testing.T, resolver DependencyResolver) {

	type csvName struct {
		name     string
		owned    []string
		required []string
	}
	var table = []struct {
		description       string
		namespace         string
		mainCSV           string
		csvs              []csvName
		crds              []string
		packageManifests  []registry.PackageManifest
		existingCRDOwners map[string][]string
		expectedErr       error
		expectedResources map[registry.ResourceKey]struct{}
	}{
		{
			"MultipleCRDOwners",
			"default",
			"macaroni-stable",
			[]csvName{
				{"macaroni-stable", []string{"macaroni"}, []string{"cheese"}},
				{"cheese-alpha", []string{"cheese"}, nil},
				{"cheese-beta", []string{"cheese"}, nil},
				{"cheese-stable", []string{"cheese"}, nil},
			},
			[]string{"macaroni", "cheese"},
			[]registry.PackageManifest{
				{
					PackageName: "cheese",
					Channels: []registry.PackageChannel{
						{
							Name:           "alpha",
							CurrentCSVName: "cheese-alpha",
						},
						{
							Name:           "beta",
							CurrentCSVName: "cheese-beta",
						},
						{
							Name:           "stable",
							CurrentCSVName: "cheese-stable",
						},
					},
					DefaultChannelName: "stable",
				},
				{
					PackageName: "macaroni",
					Channels: []registry.PackageChannel{
						{
							Name:           "stable",
							CurrentCSVName: "macaroni-stable",
						},
					},
					DefaultChannelName: "stable",
				},
			},
			nil,
			nil,
			map[registry.ResourceKey]struct{}{
				registry.ResourceKey{Name: "macaroni-stable", Kind: csvKind}:        {},
				registry.ResourceKey{Name: "macaroni", Kind: crdKind}:               {},
				registry.ResourceKey{Name: "edit-macaroni-v1", Kind: "ClusterRole"}: {},
				registry.ResourceKey{Name: "view-macaroni-v1", Kind: "ClusterRole"}: {},
				registry.ResourceKey{Name: "cheese-stable", Kind: csvKind}:          {},
				registry.ResourceKey{Name: "cheese", Kind: crdKind}:                 {},
				registry.ResourceKey{Name: "edit-cheese-v1", Kind: "ClusterRole"}:   {},
				registry.ResourceKey{Name: "view-cheese-v1", Kind: "ClusterRole"}:   {},
			},
		},
		{
			"MultipleCRDOwnersWithOnePreExisting",
			"default",
			"macaroni-stable",
			[]csvName{
				{"macaroni-stable", []string{"macaroni"}, []string{"cheese"}},
				{"cheese-alpha", []string{"cheese"}, nil},
				{"cheese-beta", []string{"cheese"}, nil},
				{"cheese-stable", []string{"cheese"}, nil},
			},
			[]string{"macaroni", "cheese"},
			[]registry.PackageManifest{
				{
					PackageName: "cheese",
					Channels: []registry.PackageChannel{
						{
							Name:           "alpha",
							CurrentCSVName: "cheese-alpha",
						},
						{
							Name:           "beta",
							CurrentCSVName: "cheese-beta",
						},
						{
							Name:           "stable",
							CurrentCSVName: "cheese-stable",
						},
					},
					DefaultChannelName: "stable",
				},
				{
					PackageName: "macaroni",
					Channels: []registry.PackageChannel{
						{
							Name:           "stable",
							CurrentCSVName: "macaroni-stable",
						},
					},
					DefaultChannelName: "stable",
				},
			},
			map[string][]string{
				"cheese": {"cheese-alpha"},
			},
			nil,
			map[registry.ResourceKey]struct{}{
				registry.ResourceKey{Name: "macaroni-stable", Kind: csvKind}:        {},
				registry.ResourceKey{Name: "macaroni", Kind: crdKind}:               {},
				registry.ResourceKey{Name: "edit-macaroni-v1", Kind: "ClusterRole"}: {},
				registry.ResourceKey{Name: "view-macaroni-v1", Kind: "ClusterRole"}: {},
				registry.ResourceKey{Name: "cheese-alpha", Kind: csvKind}:           {},
				registry.ResourceKey{Name: "cheese", Kind: crdKind}:                 {},
				registry.ResourceKey{Name: "edit-cheese-v1", Kind: "ClusterRole"}:   {},
				registry.ResourceKey{Name: "view-cheese-v1", Kind: "ClusterRole"}:   {},
			},
		},
		{
			"MultipleCRDOwnersWithTwoPreExisting",
			"default",
			"macaroni-stable",
			[]csvName{
				{"macaroni-stable", []string{"macaroni"}, []string{"cheese"}},
				{"cheese-alpha", []string{"cheese"}, nil},
				{"cheese-beta", []string{"cheese"}, nil},
				{"cheese-stable", []string{"cheese"}, nil},
			},
			[]string{"macaroni", "cheese"},
			[]registry.PackageManifest{
				{
					PackageName: "cheese",
					Channels: []registry.PackageChannel{
						{
							Name:           "alpha",
							CurrentCSVName: "cheese-alpha",
						},
						{
							Name:           "beta",
							CurrentCSVName: "cheese-beta",
						},
						{
							Name:           "stable",
							CurrentCSVName: "cheese-stable",
						},
					},
					DefaultChannelName: "stable",
				},
				{
					PackageName: "macaroni",
					Channels: []registry.PackageChannel{
						{
							Name:           "stable",
							CurrentCSVName: "macaroni-stable",
						},
					},
					DefaultChannelName: "stable",
				},
			},
			map[string][]string{
				"cheese": []string{"cheese-alpha", "cheese-beta"},
			},
			olmerrors.NewMultipleExistingCRDOwnersError([]string{"cheese-alpha", "cheese-beta"}, "cheese", "default"),
			nil,
		},
	}

	for _, tt := range table {
		t.Run(tt.description, func(t *testing.T) {
			log.SetLevel(log.DebugLevel)

			// Create a plan that is attempting to install the planCSVName.
			plan := installPlan(tt.namespace, "macaroni-stable")

			// Create catalog source
			source := registry.NewInMem()

			// Add CRDs and CSVs
			for _, name := range tt.crds {
				err := source.SetCRDDefinition(crd(name, tt.namespace))
				require.NoError(t, err)
			}
			for _, name := range tt.csvs {
				// We add unsafe so that we can test invalid states
				source.AddOrReplaceService(csv(name.name, tt.namespace, name.owned, name.required))
			}

			// Add all package manifests to the catalog
			for _, manifest := range tt.packageManifests {
				require.NoError(t, source.AddPackageManifest(manifest))
			}

			// Generate an ordered list of source refs
			srcRefs := []registry.SourceRef{
				registry.SourceRef{
					SourceKey: registry.ResourceKey{
						Name:      "pasta-source",
						Namespace: tt.namespace,
					},
					Source: source,
				},
			}

			// Resolve the plan
			steps, _, err := resolver.ResolveInstallPlan(srcRefs, tt.existingCRDOwners, "alm-catalog", &plan)

			// Set the plan and used Sources
			plan.Status.Plan = steps

			// Assert the error is as expected
			if tt.expectedErr == nil {
				require.Nil(t, err)
			} else {
				require.Equal(t, tt.expectedErr, err)
			}

			require.Equal(t, len(tt.expectedResources), len(plan.Status.Plan))

			// Assert that all steps are expected
			for _, step := range plan.Status.Plan {
				resourceKey := registry.ResourceKey{Name: step.Resource.Name, Kind: step.Resource.Kind}
				_, ok := tt.expectedResources[resourceKey]
				require.Equal(t, true, ok)
			}
		})
	}

}

func TestMultiSourceResolveInstallPlan(t *testing.T) {
	resolver := &MultiSourceResolver{}

	// Test single catalog source resolution
	resolveInstallPlan(t, resolver)

	// Test multiple catalog source resolution
	multiSourceResolveInstallPlan(t, resolver)

	// Test namespace and channel awareness
	namespaceAndChannelAwareResolveInstallPlan(t, resolver)

}

func installPlan(namespace string, names ...string) v1alpha1.InstallPlan {
	return v1alpha1.InstallPlan{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
		Spec: v1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: names,
		},
		Status: v1alpha1.InstallPlanStatus{
			Plan: []v1alpha1.Step{},
		},
	}
}

func csv(name, namespace string, owned, required []string) v1alpha1.ClusterServiceVersion {
	requiredCRDDescs := make([]v1alpha1.CRDDescription, 0)
	for _, name := range required {
		requiredCRDDescs = append(requiredCRDDescs, v1alpha1.CRDDescription{Name: name, Version: "v1", Kind: name})
	}

	ownedCRDDescs := make([]v1alpha1.CRDDescription, 0)
	for _, name := range owned {
		ownedCRDDescs = append(ownedCRDDescs, v1alpha1.CRDDescription{Name: name, Version: "v1", Kind: name})
	}

	return v1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		TypeMeta: metav1.TypeMeta{
			Kind: v1alpha1.ClusterServiceVersionKind,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned:    ownedCRDDescs,
				Required: requiredCRDDescs,
			},
		},
	}
}

func crd(name, namespace string) v1beta1.CustomResourceDefinition {
	return v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		TypeMeta: metav1.TypeMeta{
			Kind: crdKind,
		},
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group:   name + "group",
			Version: "v1",
			Names: v1beta1.CustomResourceDefinitionNames{
				Kind: name,
			},
		},
	}
}

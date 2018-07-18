package resolver

import (
	"errors"
	"testing"

	log "github.com/sirupsen/logrus"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	csvv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/clusterserviceversion/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/installplan/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/stretchr/testify/require"
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
		{"FoundCSVWithCRD", "name", []csvNames{{"name", []string{"CRD"}, nil}}, []string{"CRD"}, nil, 2},
		{"FoundCSVWithDependency", "name", []csvNames{{"name", nil, []string{"CRD"}}, {"crdOwner", []string{"CRD"}, nil}}, []string{"CRD"}, nil, 3},
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

			srcKey := registry.SourceKey{
				Name:      "tectonic-ocs",
				Namespace: plan.Namespace,
			}

			srcMap := map[registry.SourceKey]registry.Source{
				srcKey: src,
			}

			// Resolve the plan
			steps, err := resolver.ResolveInstallPlan(srcMap, srcKey, "alm-catalog", &plan)
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
				require.Equal(t, step.Resource.CatalogSource, "tectonic-ocs")
				require.Equal(t, step.Resource.CatalogSourceNamespace, plan.Namespace)
			}
		})
	}
}

func multiSourceResolveInstallPlan(t *testing.T, resolver DependencyResolver) {

	// Define some source keys representing different catalog sources (all in same namespace for now)
	sourceA := registry.SourceKey{Namespace: "default", Name: "tectonic-ocs-a"}
	sourceB := registry.SourceKey{Namespace: "default", Name: "tectonic-ocs-b"}
	sourceC := registry.SourceKey{Namespace: "default", Name: "tectonic-ocs-c"}

	type csvName struct {
		name     string
		owned    []string
		required []string
		srcKey   registry.SourceKey
	}
	type crdName struct {
		name   string
		srcKey registry.SourceKey
	}
	var table = []struct {
		description string
		planCSVName string
		csvs        map[string]csvName
		crds        map[string]crdName
		srcKeys     []registry.SourceKey
		expectedErr error
	}{
		{
			"TwoCSVsOneCRDSameCatalogSource",
			"name",
			map[string]csvName{
				"name":     {"name", nil, []string{"CRD"}, sourceA},
				"crdOwner": {"crdOwner", []string{"CRD"}, nil, sourceA},
			},
			map[string]crdName{"CRD": {"CRD", sourceA}},
			[]registry.SourceKey{sourceA},
			nil,
		},
		{
			"TwoCSVsOneCRDDifferentCatalogSources",
			"name",
			map[string]csvName{
				"name":     {"name", nil, []string{"CRD"}, sourceA},
				"crdOwner": {"crdOwner", []string{"CRD"}, nil, sourceB},
			},
			map[string]crdName{"CRD": {"CRD", sourceB}},
			[]registry.SourceKey{sourceA, sourceB},
			nil,
		},
		{
			"TwoCSVsOneCRDInSeparateCatalogSourceThanOwner",
			"name",
			map[string]csvName{
				"name":     {"name", nil, []string{"CRD"}, sourceA},
				"crdOwner": {"crdOwner", []string{"CRD"}, nil, sourceB},
			},
			map[string]crdName{"CRD": {"CRD", sourceC}},
			[]registry.SourceKey{sourceA, sourceB, sourceC},
			errors.New("not found: CRD CRD/CRD/v1"),
		},
	}

	for _, tt := range table {
		t.Run(tt.description, func(t *testing.T) {
			log.SetLevel(log.DebugLevel)
			// Create a plan that is attempting to install the planCSVName.
			plan := installPlan("test-namespace", tt.planCSVName)

			// Create catalog sources for all given srcKeys
			sources := map[registry.SourceKey]*registry.InMem{}
			var firstSrcKey registry.SourceKey
			for _, srcKey := range tt.srcKeys {
				firstSrcKey = srcKey
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

			// Generate a map of (registry.SourceKey -> registrySource)
			srcMap := map[registry.SourceKey]registry.Source{}
			for srcKey, source := range sources {
				srcMap[srcKey] = source
			}

			// Resolve the plan.
			steps, err := resolver.ResolveInstallPlan(srcMap, firstSrcKey, "alm-catalog", &plan)
			plan.Status.Plan = steps

			// Assert the error is as expected
			if tt.expectedErr == nil {
				require.Nil(t, err)
			} else {
				require.Equal(t, tt.expectedErr, err)
			}

			// Assert that all StepResources have the have the correct CatalogSource set
			for _, step := range plan.Status.Plan {
				name := step.Resource.Name
				var srcKey registry.SourceKey

				// Assume a CRD and a CSV will never have the same name
				if csv, ok := tt.csvs[name]; ok {
					srcKey = csv.srcKey
				}

				if crd, ok := tt.crds[name]; ok {
					srcKey = crd.srcKey
				}

				require.Equal(t, step.Resource.CatalogSource, srcKey.Name)
				require.Equal(t, step.Resource.CatalogSourceNamespace, srcKey.Namespace)

			}
		})
	}
}

func TestSingleSourceResolveInstallPlan(t *testing.T) {
	resolver := &SingleSourceResolver{}
	resolveInstallPlan(t, resolver)
}

func TestMultiSourceResolveInstallPlan(t *testing.T) {
	resolver := &MultiSourceResolver{}

	// Test single catalog source resolution
	resolveInstallPlan(t, resolver)

	// Test multiple catalog source resolution
	multiSourceResolveInstallPlan(t, resolver)
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

func csv(name, namespace string, owned, required []string) csvv1alpha1.ClusterServiceVersion {
	requiredCRDDescs := make([]csvv1alpha1.CRDDescription, 0)
	for _, name := range required {
		requiredCRDDescs = append(requiredCRDDescs, csvv1alpha1.CRDDescription{Name: name, Version: "v1", Kind: name})
	}

	ownedCRDDescs := make([]csvv1alpha1.CRDDescription, 0)
	for _, name := range owned {
		ownedCRDDescs = append(ownedCRDDescs, csvv1alpha1.CRDDescription{Name: name, Version: "v1", Kind: name})
	}

	return csvv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: csvv1alpha1.ClusterServiceVersionSpec{
			CustomResourceDefinitions: csvv1alpha1.CustomResourceDefinitions{
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
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group:   name + "group",
			Version: "v1",
			Names: v1beta1.CustomResourceDefinitionNames{
				Kind: name,
			},
		},
	}
}

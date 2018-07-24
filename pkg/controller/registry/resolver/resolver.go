package resolver

import (
	"fmt"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/installplan/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"

	log "github.com/sirupsen/logrus"

	csvv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/clusterserviceversion/v1alpha1"
)

// DependencyResolver defines how a something that resolves dependencies (CSVs, CRDs, etc...)
// should behave
type DependencyResolver interface {
	ResolveInstallPlan(sourceRefs []registry.SourceRef, catalogLabelKey string, plan *v1alpha1.InstallPlan) ([]v1alpha1.Step, []registry.SourceKey, error)
}

// MultiSourceResolver resolves resolves dependencies from multiple CatalogSources
type MultiSourceResolver struct{}

// ResolveInstallPlan resolves the given InstallPlan with all available sources
func (resolver *MultiSourceResolver) ResolveInstallPlan(sourceRefs []registry.SourceRef, catalogLabelKey string, plan *v1alpha1.InstallPlan) ([]v1alpha1.Step, []registry.SourceKey, error) {
	srm := make(stepResourceMap)
	var usedSourceKeys []registry.SourceKey

	for _, csvName := range plan.Spec.ClusterServiceVersionNames {
		csvSRM, used, err := resolver.resolveCSV(sourceRefs, catalogLabelKey, plan.Namespace, csvName)
		if err != nil {
			// Could not resolve CSV in any source
			return nil, nil, err
		}

		srm.Combine(csvSRM)
		usedSourceKeys = append(used, usedSourceKeys...)
	}

	return srm.Plan(), usedSourceKeys, nil
}

func (resolver *MultiSourceResolver) resolveCSV(sourceRefs []registry.SourceRef, catalogLabelKey, planNamespace, csvName string) (stepResourceMap, []registry.SourceKey, error) {
	log.Debugf("resolving CSV with name: %s", csvName)

	steps := make(stepResourceMap)
	csvNamesToBeResolved := []string{csvName}
	var usedSourceKeys []registry.SourceKey

	for len(csvNamesToBeResolved) != 0 {
		// Pop off a CSV name.
		currentName := csvNamesToBeResolved[0]
		csvNamesToBeResolved = csvNamesToBeResolved[1:]

		// If this CSV is already resolved, continue.
		if _, exists := steps[currentName]; exists {
			continue
		}

		var csvSourceKey registry.SourceKey
		var csv *csvv1alpha1.ClusterServiceVersion
		var err error

		// Attempt to Get the full CSV object for the name from any
		for _, ref := range sourceRefs {
			csv, err = ref.Source.FindCSVByName(currentName)

			if err == nil {
				// Found CSV
				csvSourceKey = ref.SourceKey
				break
			}

		}

		if err != nil {
			// Couldn't find CSV in any CatalogSource
			return nil, nil, err
		}

		log.Debugf("found %#v", csv)
		usedSourceKeys = append(usedSourceKeys, csvSourceKey)

		// Resolve each owned or required CRD for the CSV.
		for _, crdDesc := range csv.GetAllCRDDescriptions() {
			// Attempt to get CRD from same catalog source CSV was found in
			step, owner, err := resolver.resolveCRDDescription(sourceRefs, catalogLabelKey, crdDesc, csv.OwnsCRD(crdDesc.Name))
			if err != nil {
				return nil, nil, err
			}

			// If a different owner was resolved, add it to the list.
			if owner != "" && owner != currentName {
				csvNamesToBeResolved = append(csvNamesToBeResolved, owner)
			} else {
				// Add the resolved step to the plan.
				steps[currentName] = append(steps[currentName], step)
			}

		}

		// Manually override the namespace and create the final step for the CSV,
		// which is for the CSV itself.
		csv.SetNamespace(planNamespace)

		// Add the sourcename as a label on the CSV, so that we know where it came from
		labels := csv.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[catalogLabelKey] = csvSourceKey.Name
		csv.SetLabels(labels)

		step, err := v1alpha1.NewStepResourceFromCSV(csv)
		if err != nil {
			return nil, nil, err
		}

		// Set the catalog source name and namespace
		step.CatalogSource = csvSourceKey.Name
		step.CatalogSourceNamespace = csvSourceKey.Namespace

		// Add the final step for the CSV to the plan.
		log.Infof("finished step: %v", step)
		steps[currentName] = append(steps[currentName], step)
	}

	return steps, usedSourceKeys, nil
}

func (resolver *MultiSourceResolver) resolveCRDDescription(sourceRefs []registry.SourceRef, catalogLabelKey string, crdDesc csvv1alpha1.CRDDescription, owned bool) (v1alpha1.StepResource, string, error) {
	log.Debugf("resolving %#v", crdDesc)

	crdKey := registry.CRDKey{
		Kind:    crdDesc.Kind,
		Name:    crdDesc.Name,
		Version: crdDesc.Version,
	}

	var crdSourceKey registry.SourceKey
	var crd *v1beta1.CustomResourceDefinition
	var source registry.Source
	var err error

	// Attempt to find the CRD in any other source if the CRD is not owned
	for _, ref := range sourceRefs {
		source = ref.Source
		crd, err = source.FindCRDByKey(crdKey)

		if err == nil {
			// Found the CRD
			crdSourceKey = ref.SourceKey
			break
		}
	}

	if err != nil {
		return v1alpha1.StepResource{}, "", err
	}

	log.Debugf("found %#v", crd)

	if owned {
		// Label CRD with catalog source
		labels := crd.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[catalogLabelKey] = crdSourceKey.Name
		crd.SetLabels(labels)

		// Add CRD Step
		step, err := v1alpha1.NewStepResourceFromCRD(crd)

		// Set the catalog source name and namespace
		step.CatalogSource = crdSourceKey.Name
		step.CatalogSourceNamespace = crdSourceKey.Namespace

		return step, "", err
	}

	csvs, err := source.ListLatestCSVsForCRD(crdKey)
	if err != nil {
		return v1alpha1.StepResource{}, "", err
	}
	if len(csvs) == 0 {
		return v1alpha1.StepResource{}, "", fmt.Errorf("Unknown CRD %s", crdKey)
	}

	// TODO: Change to lookup the CSV from the preferred or default channel.
	log.Infof("found %v owner %s", crdKey, csvs[0].CSV.Name)
	return v1alpha1.StepResource{}, csvs[0].CSV.Name, nil

}

type stepResourceMap map[string][]v1alpha1.StepResource

func (srm stepResourceMap) Plan() []v1alpha1.Step {
	steps := make([]v1alpha1.Step, 0)
	for csvName, stepResSlice := range srm {
		for _, stepRes := range stepResSlice {
			steps = append(steps, v1alpha1.Step{
				Resolving: csvName,
				Resource:  stepRes,
				Status:    v1alpha1.StepStatusUnknown,
			})
		}
	}

	return steps
}

func (srm stepResourceMap) Combine(y stepResourceMap) {
	for csvName, stepResSlice := range y {
		// Skip any redundant steps.
		if _, alreadyExists := srm[csvName]; alreadyExists {
			continue
		}

		srm[csvName] = stepResSlice
	}
}

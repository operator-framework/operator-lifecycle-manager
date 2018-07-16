package resolver

import (
	"fmt"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/installplan/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"

	log "github.com/sirupsen/logrus"

	csvv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/clusterserviceversion/v1alpha1"
)

// DependencyResolver defines how a something that resolves dependencies (CSVs, CRDs, etc...)
// should behave
type DependencyResolver interface {
	ResolveInstallPlan(sources map[registry.SourceKey]registry.Source, firstSrcKey registry.SourceKey, catalogLabel string, plan *v1alpha1.InstallPlan) ([]v1alpha1.Step, error)
	resolveCSV(sources map[registry.SourceKey]registry.Source, firstSrcKey registry.SourceKey, catalogLabel, csvName string) (stepResourceMap, error)
	resolveCRDDescription(sources map[registry.SourceKey]registry.Source, firstSrcKey registry.SourceKey, catalogLabel string, crdDesc csvv1alpha1.CRDDescription, owned bool) (v1alpha1.StepResource, string, error)
}

// SingleSourceResolver resolves dependencies from a single CatalogSource
type SingleSourceResolver struct{}

// ResolveInstallPlan resolves all dependencies for an InstallPlan
func (resolver *SingleSourceResolver) ResolveInstallPlan(sources map[registry.SourceKey]registry.Source, firstSrcKey registry.SourceKey, catalogLabel string, plan *v1alpha1.InstallPlan) ([]v1alpha1.Step, error) {
	srm := make(stepResourceMap)
	for _, csvName := range plan.Spec.ClusterServiceVersionNames {
		csvSRM, err := resolver.resolveCSV(sources, firstSrcKey, catalogLabel, csvName)
		if err != nil {
			return nil, err
		}

		srm.Combine(csvSRM)
	}

	return srm.Plan(), nil
}

func (resolver *SingleSourceResolver) resolveCSV(sources map[registry.SourceKey]registry.Source, firstSrcKey registry.SourceKey, catalogLabel, csvName string) (stepResourceMap, error) {
	log.Debugf("resolving CSV with name: %s", csvName)

	steps := make(stepResourceMap)
	csvNamesToBeResolved := []string{csvName}

	for len(csvNamesToBeResolved) != 0 {
		// Pop off a CSV name.
		currentName := csvNamesToBeResolved[0]
		csvNamesToBeResolved = csvNamesToBeResolved[1:]

		// If this CSV is already resolved, continue.
		if _, exists := steps[currentName]; exists {
			continue
		}

		// Attempt to get the first source
		source, ok := sources[firstSrcKey]
		if !ok {
			return stepResourceMap{}, fmt.Errorf("First source %s does not exist", firstSrcKey.Name)
		}

		// Get the full CSV object for the name.
		csv, err := source.FindCSVByName(currentName)
		if err != nil {
			return nil, err
		}
		log.Debugf("found %#v", csv)

		// Resolve each owned or required CRD for the CSV.
		for _, crdDesc := range csv.GetAllCRDDescriptions() {
			step, owner, err := resolver.resolveCRDDescription(sources, firstSrcKey, catalogLabel, crdDesc, csv.OwnsCRD(crdDesc.Name))
			if err != nil {
				return nil, err
			}

			// If a different owner was resolved, add it to the list.
			if owner != "" && owner != currentName {
				csvNamesToBeResolved = append(csvNamesToBeResolved, owner)
				continue
			}

			// Add the resolved step to the plan.
			steps[currentName] = append(steps[currentName], step)
		}

		// Manually override the namespace and create the final step for the CSV,
		// which is for the CSV itself.
		csv.SetNamespace(firstSrcKey.Namespace)

		// Add the sourcename as a label on the CSV, so that we know where it came from
		labels := csv.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[catalogLabel] = firstSrcKey.Name
		csv.SetLabels(labels)

		step, err := v1alpha1.NewStepResourceFromCSV(csv)
		if err != nil {
			return nil, err
		}

		// Set the CatalogSource field
		step.CatalogSource = firstSrcKey.Name

		// Add the final step for the CSV to the plan.
		log.Infof("finished step: %v", step)
		steps[currentName] = append(steps[currentName], step)
	}

	return steps, nil
}

func (resolver *SingleSourceResolver) resolveCRDDescription(sources map[registry.SourceKey]registry.Source, firstSrcKey registry.SourceKey, catalogLabel string, crdDesc csvv1alpha1.CRDDescription, owned bool) (v1alpha1.StepResource, string, error) {
	log.Debugf("resolving %#v", crdDesc)

	crdKey := registry.CRDKey{
		Kind:    crdDesc.Kind,
		Name:    crdDesc.Name,
		Version: crdDesc.Version,
	}

	// Attempt to get the first source
	source, ok := sources[firstSrcKey]
	if !ok {
		return v1alpha1.StepResource{}, "", fmt.Errorf("First source %s does not exist", firstSrcKey.Name)
	}

	crd, err := source.FindCRDByKey(crdKey)
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
		labels[catalogLabel] = firstSrcKey.Name
		crd.SetLabels(labels)

		// Add CRD Step
		step, err := v1alpha1.NewStepResourceFromCRD(crd)

		// Set the Step's CatalogSource
		step.CatalogSource = firstSrcKey.Name

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

// MultiSourceResolver resolves resolves dependencies from multiple CatalogSources
type MultiSourceResolver struct{}

// ResolveInstallPlan resolves the given InstallPlan with all available sources
func (resolver *MultiSourceResolver) ResolveInstallPlan(firstSrcKey registry.SourceKey, sources map[registry.SourceKey]registry.Source, catalogLabel string, plan *v1alpha1.InstallPlan) error {
	srm := make(stepResourceMap)
	for _, csvName := range plan.Spec.ClusterServiceVersionNames {

		// Attempt to resolve from the first CatalogSource
		csvSRM, err := resolver.resolveCSV(sources, firstSrcKey, catalogLabel, csvName)

		if err == nil {
			srm.Combine(csvSRM)
			continue
		}

		// Attempt to resolve from any other CatalogSource
		for srcKey := range sources {
			if srcKey != firstSrcKey {
				csvSRM, err = resolver.resolveCSV(sources, srcKey, catalogLabel, csvName)
				if err == nil {
					srm.Combine(csvSRM)
					break
				}
			}
		}

		if err != nil {
			return err
		}
	}

	plan.Status.Plan = srm.Plan()
	return nil
}

func (resolver *MultiSourceResolver) resolveCSV(sources map[registry.SourceKey]registry.Source, firstSrcKey registry.SourceKey, catalogLabel, csvName string) (stepResourceMap, error) {
	log.Debugf("resolving CSV with name: %s", csvName)

	steps := make(stepResourceMap)
	csvNamesToBeResolved := []string{csvName}

	for len(csvNamesToBeResolved) != 0 {
		// Pop off a CSV name.
		currentName := csvNamesToBeResolved[0]
		csvNamesToBeResolved = csvNamesToBeResolved[1:]

		// If this CSV is already resolved, continue.
		if _, exists := steps[currentName]; exists {
			continue
		}

		// registry.SourceKey for the source containing the CSV
		csvSrcKey := firstSrcKey

		source, ok := sources[firstSrcKey]
		if !ok {
			return stepResourceMap{}, fmt.Errorf("First source %s does not exist", firstSrcKey.Name)
		}

		// Attempt to Get the full CSV object for the name from the first CatalogSource
		csv, err := source.FindCSVByName(currentName)
		if err != nil {
			// Search other Catalogs for the CSV
			for srcKey, source := range sources {
				if srcKey != firstSrcKey {
					csv, err = source.FindCSVByName(currentName)

					if err == nil {
						// Found CSV
						csvSrcKey = srcKey
						break
					}
				}
			}
		}

		if err != nil {
			// Couldn't find CSV in any CatalogSource
			return nil, err
		}

		log.Debugf("found %#v", csv)

		// Resolve each owned or required CRD for the CSV.
		for _, crdDesc := range csv.GetAllCRDDescriptions() {
			// Attempt to get the CRD
			step, owner, err := resolver.resolveCRDDescription(sources, firstSrcKey, catalogLabel, crdDesc, csv.OwnsCRD(crdDesc.Name))
			if err != nil {
				return nil, err
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
		csv.SetNamespace(csvSrcKey.Namespace)

		// Add the sourcename as a label on the CSV, so that we know where it came from
		labels := csv.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[catalogLabel] = csvSrcKey.Name
		csv.SetLabels(labels)

		step, err := v1alpha1.NewStepResourceFromCSV(csv)
		if err != nil {
			return nil, err
		}

		// Set the CatalogSource field
		step.CatalogSource = csvSrcKey.Name

		// Add the final step for the CSV to the plan.
		log.Infof("finished step: %v", step)
		steps[currentName] = append(steps[currentName], step)
	}

	return steps, nil
}

func (resolver *MultiSourceResolver) resolveCRDDescription(sources map[registry.SourceKey]registry.Source, firstSrcKey registry.SourceKey, catalogLabel string, crdDesc csvv1alpha1.CRDDescription, owned bool) (v1alpha1.StepResource, string, error) {
	log.Debugf("resolving %#v", crdDesc)

	crdKey := registry.CRDKey{
		Kind:    crdDesc.Kind,
		Name:    crdDesc.Name,
		Version: crdDesc.Version,
	}

	// registry.SourceKey for source found to contain the CRD
	crdSrcKey := firstSrcKey

	// Attempt to get the first source
	source, ok := sources[firstSrcKey]
	if !ok {
		return v1alpha1.StepResource{}, "", fmt.Errorf("First source %s does not exist", firstSrcKey.Name)
	}

	// Attempt to get the the CRD in the first SourceCatalog
	crd, err := source.FindCRDByKey(crdKey)
	if err != nil {
		// Attempt to find the CRD in any other source
		for srcKey, source := range sources {
			if srcKey != firstSrcKey {
				crd, err = source.FindCRDByKey(crdKey)

				if err == nil {
					// Found the CRD
					crdSrcKey = srcKey
					break
				}
			}
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
		labels[catalogLabel] = crdSrcKey.Name
		crd.SetLabels(labels)

		// Add CRD Step
		step, err := v1alpha1.NewStepResourceFromCRD(crd)

		// Set the Step's CatalogSource
		step.CatalogSource = firstSrcKey.Name

		return step, "", err
	}

	csvs, err := sources[crdSrcKey].ListLatestCSVsForCRD(crdKey)
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

package resolver

import (
	"fmt"

	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1beta1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	olmerrors "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/errors"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

// DependencyResolver defines how a something that resolves dependencies (CSVs, CRDs, etc...)
// should behave
type DependencyResolver interface {
	ResolveInstallPlan(sourceRefs []registry.SourceRef, existingCRDOwners map[string][]string, catalogLabelKey string, plan *v1alpha1.InstallPlan) ([]v1alpha1.Step, []registry.ResourceKey, error)
}

// MultiSourceResolver resolves resolves dependencies from multiple CatalogSources
type MultiSourceResolver struct{}

// ResolveInstallPlan resolves the given InstallPlan with all available sources
func (resolver *MultiSourceResolver) ResolveInstallPlan(sourceRefs []registry.SourceRef, existingCRDOwners map[string][]string, catalogLabelKey string, plan *v1alpha1.InstallPlan) ([]v1alpha1.Step, []registry.ResourceKey, error) {
	srm := make(stepResourceMap)
	var usedSourceKeys []registry.ResourceKey

	for _, csvName := range plan.Spec.ClusterServiceVersionNames {
		csvSRM, used, err := resolver.resolveCSV(sourceRefs, existingCRDOwners, catalogLabelKey, plan.Namespace, csvName)
		if err != nil {
			// Could not resolve CSV in any source
			return nil, nil, err
		}

		srm.Combine(csvSRM)
		usedSourceKeys = append(used, usedSourceKeys...)
	}

	return srm.Plan(), usedSourceKeys, nil
}

func (resolver *MultiSourceResolver) resolveCSV(sourceRefs []registry.SourceRef, existingCRDOwners map[string][]string, catalogLabelKey, planNamespace, csvName string) (stepResourceMap, []registry.ResourceKey, error) {
	log.Debugf("resolving CSV with name: %s", csvName)

	steps := make(stepResourceMap)
	csvNamesToBeResolved := []string{csvName}
	var usedSourceKeys []registry.ResourceKey

	for len(csvNamesToBeResolved) != 0 {
		// Pop off a CSV name.
		currentName := csvNamesToBeResolved[0]
		csvNamesToBeResolved = csvNamesToBeResolved[1:]

		// If this CSV is already resolved, continue.
		if _, exists := steps[currentName]; exists {
			continue
		}

		var csvSourceKey registry.ResourceKey
		var csv *v1alpha1.ClusterServiceVersion
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

		log.Debugf("found %s", csv.GetName())
		usedSourceKeys = append(usedSourceKeys, csvSourceKey)

		// Resolve each owned or required CRD for the CSV.
		for _, crdDesc := range csv.GetAllCRDDescriptions() {
			// Attempt to get CRD from same catalog source CSV was found in
			crdSteps, owner, err := resolveCRDDescription(sourceRefs, existingCRDOwners, catalogLabelKey, planNamespace, crdDesc, csv.OwnsCRD(crdDesc.Name))
			if err != nil {
				return nil, nil, err
			}

			// If a different owner was resolved, add it to the list.
			if owner != "" && owner != currentName {
				csvNamesToBeResolved = append(csvNamesToBeResolved, owner)
			} else {
				// Add the resolved steps to the plan.
				steps[currentName] = append(steps[currentName], crdSteps...)
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
		log.Infof("finished step: %s", step.Name)
		steps[currentName] = append(steps[currentName], step)

		// Add RBAC StepResources
		rbacSteps, err := resolveRBACStepResources(csv)
		if err != nil {
			return nil, nil, err
		}
		steps[currentName] = append(steps[currentName], rbacSteps...)
	}

	return steps, usedSourceKeys, nil
}

func resolveCRDDescription(sourceRefs []registry.SourceRef, existingCRDOwners map[string][]string, catalogLabelKey, planNamespace string, crdDesc v1alpha1.CRDDescription, owned bool) ([]v1alpha1.StepResource, string, error) {
	log.Debugf("resolving %#v", crdDesc)
	var steps []v1alpha1.StepResource

	crdKey := registry.CRDKey{
		Kind:    crdDesc.Kind,
		Name:    crdDesc.Name,
		Version: crdDesc.Version,
	}

	var crdSourceKey registry.ResourceKey
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
		return nil, "", err
	}

	if owned {
		// Label CRD with catalog source
		labels := crd.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[catalogLabelKey] = crdSourceKey.Name
		crd.SetLabels(labels)

		// Add CRD Step
		crdSteps, err := v1alpha1.NewStepResourcesFromCRD(crd)
		if err != nil {
			return nil, "", err
		}

		// Set the catalog source name and namespace
		for _, s := range crdSteps {
			s.CatalogSource = crdSourceKey.Name
			s.CatalogSourceNamespace = crdSourceKey.Namespace
			steps = append(steps, s)
		}
		return steps, "", nil
	}

	csvs, err := source.ListLatestCSVsForCRD(crdKey)
	if err != nil {
		return nil, "", err
	}
	if len(csvs) == 0 {
		return nil, "", fmt.Errorf("Unknown CRD %s", crdKey)
	}

	var ownerName string
	owners := existingCRDOwners[crdKey.Name]
	switch len(owners) {
	case 0:
		// No pre-existing owner found
		for _, csv := range csvs {
			// Check for the default channel
			if csv.IsDefaultChannel {
				ownerName = csv.CSV.Name
				break
			}
		}
	case 1:
		ownerName = owners[0]
	default:
		return nil, "", olmerrors.NewMultipleExistingCRDOwnersError(owners, crdKey.Name, planNamespace)
	}

	// Check empty name
	if ownerName == "" {
		log.Infof("No preexisting CSV or default channel found for owners of CRD %v", crdKey)
		ownerName = csvs[0].CSV.Name
	}

	log.Infof("Found %v owner %s", crdKey, ownerName)
	return nil, ownerName, nil
}

// resolveRBACStepResources returns a list of step resources required to satisfy the RBAC requirements of the given CSV's InstallStrategy
func resolveRBACStepResources(csv *v1alpha1.ClusterServiceVersion) ([]v1alpha1.StepResource, error) {
	var rbacSteps []v1alpha1.StepResource

	// User a StrategyResolver to
	strategyResolver := install.StrategyResolver{}
	strategy, err := strategyResolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
	if err != nil {
		return nil, err
	}

	// Assume the strategy is for a deployment
	strategyDetailsDeployment, ok := strategy.(*install.StrategyDetailsDeployment)
	if !ok {
		return nil, fmt.Errorf("could not assert strategy implementation as deployment for CSV %s", csv.GetName())
	}

	// Track created ServiceAccount StepResources
	serviceaccounts := map[string]struct{}{}

	// Resolve Permissions as StepResources
	for i, permission := range strategyDetailsDeployment.Permissions {
		// Create ServiceAccount if necessary
		if _, ok := serviceaccounts[permission.ServiceAccountName]; !ok {
			serviceAccount := &corev1.ServiceAccount{
				TypeMeta: metav1.TypeMeta{
					Kind: "ServiceAccount",
				},
			}
			serviceAccount.SetName(permission.ServiceAccountName)
			ownerutil.AddNonBlockingOwner(serviceAccount, csv)
			step, err := v1alpha1.NewStepResourceFromObject(serviceAccount, serviceAccount.GetName())
			if err != nil {
				return nil, err
			}
			rbacSteps = append(rbacSteps, step)

			// Mark that a StepResource has been resolved for this ServiceAccount
			serviceaccounts[permission.ServiceAccountName] = struct{}{}
		}

		// Create Role
		role := &rbac.Role{
			TypeMeta: metav1.TypeMeta{
				Kind: "Role",
			},
			Rules: permission.Rules,
		}
		ownerutil.AddNonBlockingOwner(role, csv)
		role.SetName(fmt.Sprintf("%s-role-%d", csv.GetName(), i))
		step, err := v1alpha1.NewStepResourceFromObject(role, role.GetName())
		if err != nil {
			return nil, err
		}
		rbacSteps = append(rbacSteps, step)

		// Create RoleBinding
		roleBinding := &rbac.RoleBinding{
			TypeMeta: metav1.TypeMeta{
				Kind: "RoleBinding",
			},
			RoleRef: rbac.RoleRef{
				Kind:     "Role",
				Name:     role.GetName(),
				APIGroup: rbac.GroupName},
			Subjects: []rbac.Subject{{
				Kind: "ServiceAccount",
				Name: permission.ServiceAccountName,
			}},
		}
		ownerutil.AddNonBlockingOwner(roleBinding, csv)
		roleBinding.SetName(fmt.Sprintf("%s-%s-rolebinding", role.GetName(), permission.ServiceAccountName))
		step, err = v1alpha1.NewStepResourceFromObject(roleBinding, roleBinding.GetName())
		if err != nil {
			return nil, err
		}
		rbacSteps = append(rbacSteps, step)
	}

	return rbacSteps, nil
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

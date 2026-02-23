package internal

import (
	"fmt"
	"regexp"
	"strings"

	goerrors "errors"
	"github.com/blang/semver/v4"

	"github.com/operator-framework/api/pkg/manifests"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"
)

// GoodPracticesValidator validates the bundle against criteria and suggestions defined as
// good practices for bundles under the operator-framework solutions. (You might give a
// look at https://sdk.operatorframework.io/docs/best-practices/)
//
// This validator will raise an WARNING when:
//
// - The resources request for CPU and/or Memory are not defined for any of the containers found in the CSV
//
// - The channel names seems are not following the convention https://olm.operatorframework.io/docs/best-practices/channel-naming/
//
// - CRDs defined in the bundle have empty descriptions
//
// - Check if the CSV has permissions to create CRDs. Note that:
// a) "Operators should own a CRD and only one Operator should control a CRD on a cluster. Two Operators managing the same CRD is not a recommended best practice. In the case where an API exists but with multiple implementations, this is typically an example of a no-op Operator because it doesn't have any deployment or reconciliation loop to define the shared API and other Operators depend on this Operator to provide one implementation of the API, e.g. similar to PVCs or Ingress."
//
// b) "An Operator shouldn't deploy or manage other operators (such patterns are known as meta or super operators or include CRDs in its Operands). It's the Operator Lifecycle Manager's job to manage the deployment and lifecycle of operators. For further information check Dependency Resolution: https://olm.operatorframework.io/docs/concepts/olm-architecture/dependency-resolution/"
//
// WARNING: if you create CRD's via the reconciliations or via the Operands then, OLM cannot handle CRDs migration and update, validation.
// - The bundle name (CSV.metadata.name) does not follow the naming convention: <operator-name>.v<semver> e.g. memcached-operator.v0.0.1
//
// NOTE: The bundle name must be 63 characters or less because it will be used as k8s ownerref label which only allows max of 63 characters.
var GoodPracticesValidator interfaces.Validator = interfaces.ValidatorFunc(goodPracticesValidator)

func goodPracticesValidator(objs ...interface{}) (results []errors.ManifestResult) {
	for _, obj := range objs {
		switch v := obj.(type) {
		case *manifests.Bundle:
			results = append(results, validateGoodPracticesFrom(v))
		}
	}
	return results
}

func validateGoodPracticesFrom(bundle *manifests.Bundle) errors.ManifestResult {
	result := errors.ManifestResult{}
	if bundle == nil {
		result.Add(errors.ErrInvalidBundle("Bundle is nil", nil))
		return result
	}

	result.Name = bundle.Name

	if bundle.CSV == nil {
		result.Add(errors.ErrInvalidBundle("Bundle csv is nil", bundle.Name))
		return result
	}

	errs, warns := validateResourceRequests(bundle.CSV)
	warns = append(warns, validateCrdDescriptions(bundle.CSV.Spec.CustomResourceDefinitions)...)
	warns = append(warns, validateHubChannels(bundle))
	warns = append(warns, validateRBACForCRDsWith(bundle.CSV))
	warns = append(warns, checkBundleName(bundle.CSV)...)

	for _, err := range errs {
		if err != nil {
			result.Add(errors.ErrFailedValidation(err.Error(), bundle.CSV.GetName()))
		}
	}
	for _, warn := range warns {
		if warn != nil {
			result.Add(errors.WarnFailedValidation(warn.Error(), bundle.CSV.GetName()))
		}
	}

	return result
}

// validateResourceRequests will return a WARN when the resource request is not set
func validateResourceRequests(csv *operatorsv1alpha1.ClusterServiceVersion) (errs, warns []error) {
	if csv.Spec.InstallStrategy.StrategySpec.DeploymentSpecs == nil {
		errs = append(errs, goerrors.New("unable to find a deployment to install in the CSV"))
		return errs, warns
	}
	deploymentSpec := csv.Spec.InstallStrategy.StrategySpec.DeploymentSpecs

	for _, dSpec := range deploymentSpec {
		for _, c := range dSpec.Spec.Template.Spec.Containers {
			if c.Resources.Requests == nil || !(len(c.Resources.Requests.Cpu().String()) != 0 && len(c.Resources.Requests.Memory().String()) != 0) {
				msg := fmt.Errorf("unable to find the resource requests for the container: (%s). It is recommended "+
					"to ensure the resource request for CPU and Memory. Be aware that for some clusters configurations "+
					"it is required to specify requests or limits for those values. Otherwise, the system or quota may "+
					"reject Pod creation. More info: https://master.sdk.operatorframework.io/docs/best-practices/managing-resources/", c.Name)
				warns = append(warns, msg)
			}
		}
	}
	return errs, warns
}

// checkBundleName will validate the operator bundle name informed via CSV.metadata.name.
// The motivation for the following check is to ensure that operators authors knows that operator bundles names should
// follow a name and versioning convention
func checkBundleName(csv *operatorsv1alpha1.ClusterServiceVersion) []error {
	var warns []error
	// Check if is following the semver
	re := regexp.MustCompile("([0-9]+)\\.([0-9]+)\\.([0-9]+)(?:-([0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*))?(?:\\+[0-9A-Za-z-]+)?$")
	match := re.FindStringSubmatch(csv.Name)

	if len(match) > 0 {
		if _, err := semver.Parse(match[0]); err != nil {
			warns = append(warns, fmt.Errorf("csv.metadata.Name %v is not following the versioning "+
				"convention (MAJOR.MINOR.PATCH e.g 0.0.1): https://semver.org/", csv.Name))
		}
	} else {
		warns = append(warns, fmt.Errorf("csv.metadata.Name %v is not following the versioning "+
			"convention (MAJOR.MINOR.PATCH e.g 0.0.1): https://semver.org/", csv.Name))
	}

	// Check if its following the name convention
	if len(strings.Split(csv.Name, ".v")) != 2 {
		warns = append(warns, fmt.Errorf("csv.metadata.Name %v is not following the recommended "+
			"naming convention: <operator-name>.v<semver> e.g. memcached-operator.v0.0.1", csv.Name))
	}

	return warns
}

// validateHubChannels will check the channels. The motivation for the following check is to ensure that operators
// authors knows if their operator bundles are or not respecting the Naming Convention Rules.
// However, the operator authors still able to choose the names as please them.
func validateHubChannels(bundle *manifests.Bundle) error {
	channels := append(bundle.Channels, bundle.DefaultChannel)
	const candidate = "candidate"
	const stable = "stable"
	const fast = "fast"

	channels = getUniqueValues(channels)
	var channelsNotFollowingConventional []string
	for _, channel := range channels {
		if !strings.HasPrefix(channel, candidate) &&
			!strings.HasPrefix(channel, stable) &&
			!strings.HasPrefix(channel, fast) &&
			channel != "" {
			channelsNotFollowingConventional = append(channelsNotFollowingConventional, channel)
		}

	}

	if len(channelsNotFollowingConventional) > 0 {
		return fmt.Errorf("channel(s) %+q are not following the recommended naming convention: "+
			"https://olm.operatorframework.io/docs/best-practices/channel-naming",
			channelsNotFollowingConventional)
	}

	return nil
}

// validateRBACForCRDsWith to warning when/if permissions to create CRD are found in the rules
func validateRBACForCRDsWith(csv *operatorsv1alpha1.ClusterServiceVersion) error {
	apiGroupResourceMap := map[string][]string{
		"apiextensions.k8s.io": {"customresourcedefinitions", "*", "[*]"},
	}
	verbs := []string{"create", "*", "[*]", "patch"}
	warning := goerrors.New("CSV contains permissions to create CRD. An Operator shouldn't deploy or manage " +
		"other operators (such patterns are known as meta or super operators or include CRDs in its Operands)." +
		" It's the Operator Lifecycle Manager's job to manage the deployment and lifecycle of operators. " +
		" Please, review the design of your solution and if you should not be using Dependency Resolution from OLM instead." +
		" More info: https://sdk.operatorframework.io/docs/best-practices/common-recommendation/")

	for _, perm := range csv.Spec.InstallStrategy.StrategySpec.Permissions {
		if hasRBACFor(perm, apiGroupResourceMap, verbs) {
			return warning
		}
	}

	for _, perm := range csv.Spec.InstallStrategy.StrategySpec.ClusterPermissions {
		if hasRBACFor(perm, apiGroupResourceMap, verbs) {
			return warning
		}
	}

	return nil
}

func hasRBACFor(perm v1alpha1.StrategyDeploymentPermissions, apiGroupResourceMap map[string][]string, verbs []string) bool {
	// For each APIGroup and list of resources that we are looking for
	for apiFromMap, resourcesFromMap := range apiGroupResourceMap {
		for _, rule := range perm.Rules {
			for _, api := range rule.APIGroups {
				// If we found the APIGroup
				if api == apiFromMap {
					for _, res := range rule.Resources {
						for _, resFromMap := range resourcesFromMap {
							// If we found the resource
							if resFromMap == res {
								// Check if we find the verbs:
								for _, verbFromList := range verbs {
									for _, ruleVerb := range rule.Verbs {
										// If we found the verb
										if verbFromList == ruleVerb {
											// stopping by returning true
											return true
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return false
}

// getUniqueValues return the values without duplicates
func getUniqueValues(array []string) []string {
	var result []string
	uniqueValues := make(map[string]string)
	for _, n := range array {
		uniqueValues[strings.TrimSpace(n)] = ""
	}

	for k, _ := range uniqueValues {
		result = append(result, k)
	}
	return result
}

// validateCrdDescriptions ensures that all CRDs defined in the bundle have non-empty descriptions.
func validateCrdDescriptions(crds operatorsv1alpha1.CustomResourceDefinitions) []error {
	f := func(crds []operatorsv1alpha1.CRDDescription, relation string) []error {
		errors := make([]error, 0, len(crds))
		for _, crd := range crds {
			if crd.Description == "" {
				errors = append(errors, fmt.Errorf("%s CRD %q has an empty description", relation, crd.Name))
			}
		}
		return errors
	}

	return append(f(crds.Owned, "owned"), f(crds.Required, "required")...)
}

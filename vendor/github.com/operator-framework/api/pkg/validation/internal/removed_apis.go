package internal

import (
	"fmt"
	"github.com/blang/semver"
	"github.com/operator-framework/api/pkg/manifests"
	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sort"
)

// k8sVersionKey defines the key which can be used by its consumers
// to inform what is the K8S version that should be used to do the tests against.
const k8sVersionKey = "k8s-version"

// DeprecateMessage defines the content of the message that will be raised as an error or warning
// when the removed apis are found
const DeprecateMessage = "this bundle is using APIs which were deprecated and removed in v%v.%v. " +
	"More info: https://kubernetes.io/docs/reference/using-api/deprecation-guide/#v%v-%v. " +
	"Migrate the API(s) for %s"

// K8sVersionsSupportedByValidator defines the k8s versions which this validator is implemented to
// perform the checks
var K8sVersionsSupportedByValidator = []string{"1.22.0", "1.25.0", "1.26.0"}

// AlphaDeprecatedAPIsValidator implements Validator to validate bundle objects
// for API deprecation requirements.
//
// Note that this validator looks at the manifests. If any removed APIs for the mapped k8s versions are found,
// it raises a warning.
//
// This validator only raises an error when the deprecated API found is removed in the specified k8s
// version informed via the optional key `k8s-version`.
//
// The K8s versions supported and checks are:
//
// - 1.22 : https://kubernetes.io/docs/reference/using-api/deprecation-guide/#v1-22
//
// - 1.25 : https://kubernetes.io/docs/reference/using-api/deprecation-guide/#v1-25
//
// - 1.26 : https://kubernetes.io/docs/reference/using-api/deprecation-guide/#v1-26
//
// IMPORTANT: Note that in the case scenarios of 1.25 and 1.26 it is very unlikely the OperatorAuthors
// add manifests on the bundle using these APIs. On top of that some Kinds such as the CronJob
// are not currently a valid/supported by OLM and never would to be added to bundle.
// See: https://github.com/operator-framework/operator-registry/blob/v1.19.5/pkg/lib/bundle/supported_resources.go#L3-L23
var AlphaDeprecatedAPIsValidator interfaces.Validator = interfaces.ValidatorFunc(validateDeprecatedAPIsValidator)

func validateDeprecatedAPIsValidator(objs ...interface{}) (results []errors.ManifestResult) {

	// Obtain the k8s version if informed via the objects an optional
	k8sVersion := ""
	for _, obj := range objs {
		switch obj.(type) {
		case map[string]string:
			k8sVersion = obj.(map[string]string)[k8sVersionKey]
			if len(k8sVersion) > 0 {
				break
			}
		}
	}

	for _, obj := range objs {
		switch v := obj.(type) {
		case *manifests.Bundle:
			results = append(results, validateDeprecatedAPIs(v, k8sVersion))
		}
	}

	return results
}

func validateDeprecatedAPIs(bundle *manifests.Bundle, k8sVersion string) errors.ManifestResult {
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

	errs, warns := validateDeprecatedAPIS(bundle, k8sVersion)
	for _, err := range errs {
		result.Add(errors.ErrFailedValidation(err.Error(), bundle.CSV.GetName()))
	}
	for _, warn := range warns {
		result.Add(errors.WarnFailedValidation(warn.Error(), bundle.CSV.GetName()))
	}

	return result
}

// validateDeprecatedAPIS will check if the operator bundle is using a deprecated or no longer supported k8s api
// Note if the k8s version was informed via "k8s-version" optional key it will be used. Otherwise, we will use the minKubeVersion in
// the CSV to do the checks. So, the criteria is >=minKubeVersion. Lastly, if the minKubeVersion is not provided
// then, we should consider the operator bundle is intend to work well in any Kubernetes version.
// Then, it means that:
// - --optional-values="k8s-version=value" flag with a value <= unsupportedAPIVersion the validator will return result as warning.
// - --optional-values="k8s-version=value" flag with a value => unsupportedAPIVersion the validator will return result as error.
// - minKubeVersion >= unsupportedAPIVersion return the error result.
// - minKubeVersion empty returns a warning since it would mean the same of allow in any supported version
func validateDeprecatedAPIS(bundle *manifests.Bundle, versionProvided string) (errs, warns []error) {
	// isVersionProvided defines if the k8s version to test against was or not informed
	isVersionProvided := len(versionProvided) > 0
	// semVerVersionProvided -- converts the k8s version informed in semver
	semVerVersionProvided, _ := semver.ParseTolerant(versionProvided)

	if err := verifyK8sVersionInformed(versionProvided); err != nil && isVersionProvided {
		errs = append(errs, err)
	}

	// Transform the spec minKubeVersion in semver Version to compare
	var semverMinKube semver.Version
	if len(bundle.CSV.Spec.MinKubeVersion) > 0 {
		var err error
		if semverMinKube, err = semver.ParseTolerant(bundle.CSV.Spec.MinKubeVersion); err != nil {
			errs = append(errs, fmt.Errorf("unable to use csv.Spec.MinKubeVersion to verify the CRD/Webhook apis "+
				"because it has an invalid value: %s", bundle.CSV.Spec.MinKubeVersion))
		}
	}

	// Check the bundle with all k8s versions implemented
	for _, v := range K8sVersionsSupportedByValidator {
		k8sVersionToCheck := semver.MustParse(v)
		errs, warns = checkRemovedAPIsForVersion(bundle,
			k8sVersionToCheck,
			semVerVersionProvided,
			semverMinKube,
			errs,
			warns)
	}

	return errs, warns
}

// checkRemovedAPIsForVersion will check if the bundle is using the removed APIs
// for the version informed (k8sVersionToCheck)
func checkRemovedAPIsForVersion(
	bundle *manifests.Bundle,
	k8sVersionToCheck, semVerVersionProvided, semverMinKube semver.Version,
	errs []error, warns []error) ([]error, []error) {

	found := map[string][]string{}
	switch k8sVersionToCheck.String() {
	case "1.22.0":
		found = getRemovedAPIsOn1_22From(bundle)
	case "1.25.0":
		found = getRemovedAPIsOn1_25From(bundle)
	case "1.26.0":
		found = getRemovedAPIsOn1_26From(bundle)
	default:
		panic(fmt.Errorf("invalid internal call to check the removed apis with the version (%s) which is not supported", k8sVersionToCheck.String()))
	}

	if len(found) > 0 {
		deprecatedAPIsMessage := generateMessageWithDeprecatedAPIs(found)
		msg := fmt.Errorf(DeprecateMessage,
			k8sVersionToCheck.Major, k8sVersionToCheck.Minor,
			k8sVersionToCheck.Major, k8sVersionToCheck.Minor,
			deprecatedAPIsMessage)
		if isK8sVersionInformedEQ(semVerVersionProvided, k8sVersionToCheck, semverMinKube) {
			// We only raise an error when the version >= 1.26 was informed via
			// the k8s key/value option or is specifically defined in the CSV
			errs = append(errs, msg)
		} else {
			warns = append(warns, msg)
		}
	}
	return errs, warns
}

// isK8sVersionInformedEQ returns true only if the key/value OR minKubeVersion were informed and are >= semVerAPIUnsupported
func isK8sVersionInformedEQ(semVerVersionProvided semver.Version, semVerAPIUnsupported semver.Version, semverMinKube semver.Version) bool {
	return semVerVersionProvided.GE(semVerAPIUnsupported) || semverMinKube.GE(semVerAPIUnsupported)
}

func verifyK8sVersionInformed(versionProvided string) error {
	if _, err := semver.ParseTolerant(versionProvided); err != nil {
		return fmt.Errorf("invalid value informed via the k8s key option : %s", versionProvided)
	}
	return nil
}

// generateMessageWithDeprecatedAPIs will return a list with the kind and the name
// of the resource which were found and required to be upgraded
func generateMessageWithDeprecatedAPIs(deprecatedAPIs map[string][]string) string {
	msg := ""
	count := 0

	keys := make([]string, 0, len(deprecatedAPIs))
	for k := range deprecatedAPIs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	deprecatedAPIsSorted := make(map[string][]string)
	for _, key := range keys {
		deprecatedAPIsSorted[key] = deprecatedAPIs[key]
	}

	for k, v := range deprecatedAPIsSorted {
		if count == len(deprecatedAPIs)-1 {
			msg = msg + fmt.Sprintf("%s: (%+q)", k, v)
		} else {
			msg = msg + fmt.Sprintf("%s: (%+q),", k, v)
		}
	}
	return msg
}

// getRemovedAPIsOn1_22From return the list of resources which were deprecated
// and are no longer be supported in 1.22. More info: https://kubernetes.io/docs/reference/using-api/deprecation-guide/#v1-22
func getRemovedAPIsOn1_22From(bundle *manifests.Bundle) map[string][]string {
	deprecatedAPIs := make(map[string][]string)
	if len(bundle.V1beta1CRDs) > 0 {
		var crdApiNames []string
		for _, obj := range bundle.V1beta1CRDs {
			crdApiNames = append(crdApiNames, obj.Name)
		}
		deprecatedAPIs["CRD"] = crdApiNames
	}

	for _, obj := range bundle.Objects {
		switch u := obj.GetObjectKind().(type) {
		case *unstructured.Unstructured:
			switch u.GetAPIVersion() {
			case "scheduling.k8s.io/v1beta1":
				if u.GetKind() == PriorityClassKind {
					deprecatedAPIs[u.GetKind()] = append(deprecatedAPIs[u.GetKind()], obj.GetName())
				}
			case "rbac.authorization.k8s.io/v1beta1":
				if u.GetKind() == RoleKind || u.GetKind() == "ClusterRoleBinding" || u.GetKind() == "RoleBinding" || u.GetKind() == ClusterRoleKind {
					deprecatedAPIs[u.GetKind()] = append(deprecatedAPIs[u.GetKind()], obj.GetName())
				}
			case "apiregistration.k8s.io/v1beta1":
				if u.GetKind() == "APIService" {
					deprecatedAPIs[u.GetKind()] = append(deprecatedAPIs[u.GetKind()], obj.GetName())
				}
			case "authentication.k8s.io/v1beta1":
				if u.GetKind() == "TokenReview" {
					deprecatedAPIs[u.GetKind()] = append(deprecatedAPIs[u.GetKind()], obj.GetName())
				}
			case "authorization.k8s.io/v1beta1":
				if u.GetKind() == "LocalSubjectAccessReview" || u.GetKind() == "SelfSubjectAccessReview" || u.GetKind() == "SubjectAccessReview" {
					deprecatedAPIs[u.GetKind()] = append(deprecatedAPIs[u.GetKind()], obj.GetName())
				}
			case "admissionregistration.k8s.io/v1beta1":
				if u.GetKind() == "MutatingWebhookConfiguration" || u.GetKind() == "ValidatingWebhookConfiguration" {
					deprecatedAPIs[u.GetKind()] = append(deprecatedAPIs[u.GetKind()], obj.GetName())
				}
			case "coordination.k8s.io/v1beta1":
				if u.GetKind() == "Lease" {
					deprecatedAPIs[u.GetKind()] = append(deprecatedAPIs[u.GetKind()], obj.GetName())
				}
			case "extensions/v1beta1":
				if u.GetKind() == "Ingress" {
					deprecatedAPIs[u.GetKind()] = append(deprecatedAPIs[u.GetKind()], obj.GetName())
				}
			case "networking.k8s.io/v1beta1":
				if u.GetKind() == "Ingress" || u.GetKind() == "IngressClass" {
					deprecatedAPIs[u.GetKind()] = append(deprecatedAPIs[u.GetKind()], obj.GetName())
				}
			case "storage.k8s.io/v1beta1":
				if u.GetKind() == "CSIDriver" || u.GetKind() == "CSINode" || u.GetKind() == "StorageClass" || u.GetKind() == "VolumeAttachment" {
					deprecatedAPIs[u.GetKind()] = append(deprecatedAPIs[u.GetKind()], obj.GetName())
				}
			case "certificates.k8s.io/v1beta1":
				if u.GetKind() == "CertificateSigningRequest" {
					deprecatedAPIs[u.GetKind()] = append(deprecatedAPIs[u.GetKind()], obj.GetName())
				}
			}
		}
	}
	return deprecatedAPIs
}

// getRemovedAPIsOn1_25From return the list of resources which were deprecated
// and are no longer be supported in 1.25. More info: https://kubernetes.io/docs/reference/using-api/deprecation-guide/#v1-25
//
// IMPORTANT: Note that in the case scenarios of 1.25 and 1.26 it is very unlikely the OperatorAuthors
// add manifests on the bundle using these APIs. On top of that some Kinds such as the CronJob
// are not currently a valid/supported by OLM and never would to be added to bundle.
// See: https://github.com/operator-framework/operator-registry/blob/v1.19.5/pkg/lib/bundle/supported_resources.go#L3-L23
func getRemovedAPIsOn1_25From(bundle *manifests.Bundle) map[string][]string {
	deprecatedAPIs := make(map[string][]string)
	for _, obj := range bundle.Objects {
		switch u := obj.GetObjectKind().(type) {
		case *unstructured.Unstructured:
			switch u.GetAPIVersion() {
			case "batch/v1beta1":
				if u.GetKind() == "CronJob" {
					deprecatedAPIs[u.GetKind()] = append(deprecatedAPIs[u.GetKind()], obj.GetName())
				}
			case "discovery.k8s.io/v1beta1":
				if u.GetKind() == "EndpointSlice" {
					deprecatedAPIs[u.GetKind()] = append(deprecatedAPIs[u.GetKind()], obj.GetName())
				}
			case "events.k8s.io/v1beta1":
				if u.GetKind() == "Event" {
					deprecatedAPIs[u.GetKind()] = append(deprecatedAPIs[u.GetKind()], obj.GetName())
				}
			case "autoscaling/v2beta1":
				if u.GetKind() == "HorizontalPodAutoscaler" {
					deprecatedAPIs[u.GetKind()] = append(deprecatedAPIs[u.GetKind()], obj.GetName())
				}
			case "policy/v1beta1":
				if u.GetKind() == "PodDisruptionBudget" || u.GetKind() == "PodSecurityPolicy" {
					deprecatedAPIs[u.GetKind()] = append(deprecatedAPIs[u.GetKind()], obj.GetName())
				}
			case "node.k8s.io/v1beta1":
				if u.GetKind() == "RuntimeClass" {
					deprecatedAPIs[u.GetKind()] = append(deprecatedAPIs[u.GetKind()], obj.GetName())
				}
			}
		}
	}
	return deprecatedAPIs
}

// getRemovedAPIsOn1_26From return the list of resources which were deprecated
// and are no longer be supported in 1.26. More info: https://kubernetes.io/docs/reference/using-api/deprecation-guide/#v1-26
//
// IMPORTANT: Note that in the case scenarios of 1.25 and 1.26 it is very unlikely the OperatorAuthors
// add manifests on the bundle using these APIs. On top of that some Kinds such as the CronJob
// are not currently a valid/supported by OLM and never would to be added to bundle.
// See: https://github.com/operator-framework/operator-registry/blob/v1.19.5/pkg/lib/bundle/supported_resources.go#L3-L23
func getRemovedAPIsOn1_26From(bundle *manifests.Bundle) map[string][]string {
	deprecatedAPIs := make(map[string][]string)
	for _, obj := range bundle.Objects {
		switch u := obj.GetObjectKind().(type) {
		case *unstructured.Unstructured:
			switch u.GetAPIVersion() {
			case "autoscaling/v2beta2":
				if u.GetKind() == "HorizontalPodAutoscaler" {
					deprecatedAPIs[u.GetKind()] = append(deprecatedAPIs[u.GetKind()], obj.GetName())
				}
			}
		}
	}
	return deprecatedAPIs
}

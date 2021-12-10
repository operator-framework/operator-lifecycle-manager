package internal

import (
	"fmt"
	"github.com/blang/semver"

	"github.com/operator-framework/api/pkg/manifests"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"
)

// k8sVersionKey defines the key which can be used by its consumers
// to inform what is the K8S version that should be used to do the tests against.
const k8sVersionKey = "k8s-version"

const minKubeVersionWarnMessage = "csv.Spec.minKubeVersion is not informed. It is recommended you provide this information. " +
	"Otherwise, it would mean that your operator project can be distributed and installed in any cluster version " +
	"available, which is not necessarily the case for all projects."

// K8s version where the apis v1betav1 is no longer supported
const k8sVerV1betav1Unsupported = "1.22.0"

// K8s version where the apis v1betav1 was deprecated
const k8sVerV1betav1Deprecated = "1.16.0"

// AlphaDeprecatedAPIsValidator validates if the bundles is using versions API version which are deprecate or
// removed in specific Kubernetes versions informed via optional key value `k8s-version`.
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
	result := errors.ManifestResult{Name: bundle.Name}

	if bundle == nil {
		result.Add(errors.ErrInvalidBundle("Bundle is nil", nil))
		return result
	}

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
// Note if the k8s was informed via "k8s=1.22" it will be used. Otherwise, we will use the minKubeVersion in
// the CSV to do the checks. So, the criteria is >=minKubeVersion. By last, if the minKubeVersion is not provided
// then, we should consider the operator bundle is intend to work well in any Kubernetes version.
// Then, it means that:
//--optional-values="k8s-version=value" flag with a value => 1.16 <= 1.22 the validator will return result as warning.
//--optional-values="k8s-version=value" flag with a value => 1.22 the validator will return result as error.
//minKubeVersion >= 1.22 return the error result.
//minKubeVersion empty returns a warning since it would mean the same of allow install in any supported version
func validateDeprecatedAPIS(bundle *manifests.Bundle, versionProvided string) (errs, warns []error) {

	// semver of the K8s version where the apis v1betav1 is no longer supported to allow us compare
	semVerK8sVerV1betav1Unsupported := semver.MustParse(k8sVerV1betav1Unsupported)
	// semver of the K8s version where the apis v1betav1 is deprecated to allow us compare
	semVerk8sVerV1betav1Deprecated := semver.MustParse(k8sVerV1betav1Deprecated)
	// isVersionProvided defines if the k8s version to test against was or not informed
	isVersionProvided := len(versionProvided) > 0

	// Transform the key/option versionProvided in semver Version to compare
	var semVerVersionProvided semver.Version
	if isVersionProvided {
		var err error
		semVerVersionProvided, err = semver.ParseTolerant(versionProvided)
		if err != nil {
			errs = append(errs, fmt.Errorf("invalid value informed via the k8s key option : %s", versionProvided))
		} else {
			// we might want to return it as info instead of warning in the future.
			warns = append(warns, fmt.Errorf("checking APIs against Kubernetes version : %s", versionProvided))
		}
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

	// if the k8s value was informed and it is >=1.16 we should check
	// if the k8s value was not informed we also should check since the
	// check should occurs with any minKubeVersion value:
	// - if minKubeVersion empty then means that the project can be installed in any version
	// - if minKubeVersion any version defined it means that we are considering install
	// in any upper version from that where the check is always applied
	if !isVersionProvided || semVerVersionProvided.GE(semVerk8sVerV1betav1Deprecated) {
		deprecatedAPIs := getRemovedAPIsOn1_22From(bundle)
		if len(deprecatedAPIs) > 0 {
			deprecatedAPIsMessage := generateMessageWithDeprecatedAPIs(deprecatedAPIs)
			// isUnsupported is true only if the key/value OR minKubeVersion were informed and are >= 1.22
			isUnsupported := semVerVersionProvided.GE(semVerK8sVerV1betav1Unsupported) ||
				semverMinKube.GE(semVerK8sVerV1betav1Unsupported)
			// We only raise an error when the version >= 1.22 was informed via
			// the k8s key/value option or is specifically defined in the CSV
			msg := fmt.Errorf("this bundle is using APIs which were deprecated and removed in v1.22. More info: https://kubernetes.io/docs/reference/using-api/deprecation-guide/#v1-22. Migrate the API(s) for %s", deprecatedAPIsMessage)
			if isUnsupported {
				errs = append(errs, msg)
			} else {
				warns = append(warns, msg)
			}
		}
	}

	return errs, warns
}

// generateMessageWithDeprecatedAPIs will return a list with the kind and the name
// of the resource which were found and required to be upgraded
func generateMessageWithDeprecatedAPIs(deprecatedAPIs map[string][]string) string {
	msg := ""
	count := 0
	for k, v := range deprecatedAPIs {
		if count == len(deprecatedAPIs)-1 {
			msg = msg + fmt.Sprintf("%s: (%+q)", k, v)
		} else {
			msg = msg + fmt.Sprintf("%s: (%+q),", k, v)
		}
	}
	return msg
}

// todo: we need to improve this code since we ought to map the kinds, apis and ocp/k8s versions
// where them are no longer supported ( removed ) instead of have this fixed in this way.

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

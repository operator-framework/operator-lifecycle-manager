package internal

import (
	"fmt"

	"github.com/operator-framework/api/pkg/manifests"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// OCP version where the apis v1beta1 is no longer supported
const ocpVerV1beta1Unsupported = "4.9"

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

package catalog

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// deprecatedError is returned from installplan execution when a step contains a resource in a GroupVersion that has been deprecated.
type deprecatedError struct {
	gv   schema.GroupVersion
	k    string
	name string
}

func (d deprecatedError) Error() string {
	return fmt.Sprintf("resource has been deprecated: %s of Kind %s is in a deprecated GroupVersion %s and is no longer installable with this version of OLM. %s", d.name, d.k, d.gv,
		"See https://kubernetes.io/docs/reference/using-api/deprecation-guide/ for information on how to update to a supported version of this resource.")
}

// deprecatedGroupVersions contains a list of OLM-supported GroupVersions that have been deprecated and are now removed in kube 1.22+.
// Any resources in these GVs are no longer installable on a 1.22+ cluster. OLM will return an install plan error if a resource in any
// of these group/versions is found in an installplan step before attempting to create it on-cluster.
// See https://kubernetes.io/docs/reference/using-api/deprecation-guide/ for notes on deprecated resources.
// As additional resource groups move from deprecated to removed this list may need to be updated.
var deprecatedGroupVersions = map[schema.GroupVersion]struct{}{
	schema.GroupVersion{Group: "admissionregistration.k8s.io", Version: "v1beta1"}: {},
	schema.GroupVersion{Group: "apiextensions.k8s.io", Version: "v1beta1"}:         {},
	schema.GroupVersion{Group: "apiregistration.k8s.io", Version: "v1beta1"}:       {},
	schema.GroupVersion{Group: "rbac.authorization.k8s.io", Version: "v1beta1"}:    {},
	schema.GroupVersion{Group: "scheduling.k8s.io", Version: "v1beta1"}:            {},
}

func deprecated(gv schema.GroupVersion) bool {
	_, ok := deprecatedGroupVersions[gv]
	return ok
}

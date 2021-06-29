package catalog

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

type deprecatedError struct {
	gv   schema.GroupVersion
	k    string
	name string
}

func (d deprecatedError) Error() string {
	return fmt.Sprintf("resource has been deprecated: %s of Kind %s is in a deprecated GroupVersion %s and is no longer installable with this version of OLM. %s", d.name, d.k, d.gv,
		"See https://kubernetes.io/docs/reference/using-api/deprecation-guide/ for information on how to update to a supported version of this resource.")
}

// deprecatedGroupVersions contains a list of OLM-supported resources that are now deprecated
// and no longer installable on cluster. OLM will return an install plan error if a resource in any
// of these group/versions is found in an installplan step before attempting to create it on-cluster.
// See https://kubernetes.io/docs/reference/using-api/deprecation-guide/ for notes on deprecated resources.
var deprecatedGroupVersions = map[schema.GroupVersion]struct{}{
	schema.GroupVersion{Group: "admissionregistration.k8s.io", Version: "v1beta1"}:      {},
	schema.GroupVersion{Group: "apiextensions.k8s.io/v1beta1", Version: "v1beta1"}:      {},
	schema.GroupVersion{Group: "apiregistration.k8s.io/v1beta1", Version: "v1beta1"}:    {},
	schema.GroupVersion{Group: "rbac.authorization.k8s.io/v1beta1", Version: "v1beta1"}: {},
	schema.GroupVersion{Group: "scheduling.k8s.io/v1beta1", Version: "v1beta1"}:         {},
}

func deprecated(gv schema.GroupVersion) bool {
	_, ok := deprecatedGroupVersions[gv]
	return ok
}

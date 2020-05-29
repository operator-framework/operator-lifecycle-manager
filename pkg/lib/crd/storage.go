package crd

import (
	"fmt"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
)

// SafeStorageVersionUpgrade determines whether the new CRD spec includes all the storage versions of the existing on-cluster CRD.
// For each stored version in the status of the CRD on the cluster (there will be at least one) - each version must exist in the spec of the new CRD that is being installed.
// See https://kubernetes.io/docs/tasks/access-kubernetes-api/custom-resources/custom-resource-definition-versioning/#upgrade-existing-objects-to-a-new-stored-version.
func SafeStorageVersionUpgrade(existingCRD runtime.Object, newCRD runtime.Object) (bool, error) {
	newSpecVersions, existingStatusVersions := getStoredVersions(existingCRD, newCRD)
	if newSpecVersions == nil {
		return true, fmt.Errorf("could not find any versions in the new CRD")
	}
	if existingStatusVersions == nil {
		// every on-cluster CRD should have at least one stored version in its status
		// in the case where there are no existing stored versions, checking against new versions is not relevant
		return true, nil
	}

	for name := range existingStatusVersions {
		if _, ok := newSpecVersions[name]; !ok {
			// a storage version in the status of the old CRD is not present in the spec of the new CRD
			// potential data loss of CRs without a storage migration - throw error and block the CRD upgrade
			return false, fmt.Errorf("new CRD removes version %s that is listed as a stored version on the existing CRD", name)
		}
	}

	return true, nil
}

// getStoredVersions returns the storage versions listed in the status of the old on-cluster CRD
// and all the versions listed in the spec of the new CRD.
func getStoredVersions(oldCRD runtime.Object, newCRD runtime.Object) (newSpecVersions map[string]struct{}, existingStatusVersions map[string]struct{}) {
	existingStatusVersions = make(map[string]struct{})
	newSpecVersions = make(map[string]struct{})

	// find old storage versions by inspect the status field of the existing on-cluster CRD
	switch crd := oldCRD.(type) {
	case *apiextensionsv1.CustomResourceDefinition:
		for _, version := range crd.Status.StoredVersions {
			existingStatusVersions[version] = struct{}{}
		}
	case *apiextensionsv1beta1.CustomResourceDefinition:
		for _, version := range crd.Status.StoredVersions {
			existingStatusVersions[version] = struct{}{}
		}
	}

	switch crd := newCRD.(type) {
	case *apiextensionsv1.CustomResourceDefinition:
		for _, version := range crd.Spec.Versions {
			newSpecVersions[version.Name] = struct{}{}
		}
	case *apiextensionsv1beta1.CustomResourceDefinition:
		if crd.Spec.Version != "" {
			newSpecVersions[crd.Spec.Version] = struct{}{}
		}
		for _, version := range crd.Spec.Versions {
			newSpecVersions[version.Name] = struct{}{}
		}
	}

	return newSpecVersions, existingStatusVersions
}

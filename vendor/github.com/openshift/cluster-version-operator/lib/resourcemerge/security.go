package resourcemerge

import (
	securityv1 "github.com/openshift/api/security/v1"
	"k8s.io/apimachinery/pkg/api/equality"
)

// EnsureSecurityContextConstraints ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureSecurityContextConstraints(modified *bool, existing *securityv1.SecurityContextConstraints, required securityv1.SecurityContextConstraints) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)
	setInt32Ptr(modified, &existing.Priority, required.Priority)
	setBool(modified, &existing.AllowPrivilegedContainer, required.AllowPrivilegedContainer)
	for _, required := range required.DefaultAddCapabilities {
		found := false
		for _, curr := range existing.DefaultAddCapabilities {
			if equality.Semantic.DeepEqual(required, curr) {
				found = true
				break
			}
		}
		if !found {
			*modified = true
			existing.DefaultAddCapabilities = append(existing.DefaultAddCapabilities, required)
		}
	}
	for _, required := range required.RequiredDropCapabilities {
		found := false
		for _, curr := range existing.RequiredDropCapabilities {
			if equality.Semantic.DeepEqual(required, curr) {
				found = true
				break
			}
		}
		if !found {
			*modified = true
			existing.RequiredDropCapabilities = append(existing.RequiredDropCapabilities, required)
		}
	}
	for _, required := range required.AllowedCapabilities {
		found := false
		for _, curr := range existing.AllowedCapabilities {
			if equality.Semantic.DeepEqual(required, curr) {
				found = true
				break
			}
		}
		if !found {
			*modified = true
			existing.AllowedCapabilities = append(existing.AllowedCapabilities, required)
		}
	}
	setBool(modified, &existing.AllowHostDirVolumePlugin, required.AllowHostDirVolumePlugin)
	for _, required := range required.Volumes {
		found := false
		for _, curr := range existing.Volumes {
			if equality.Semantic.DeepEqual(required, curr) {
				found = true
				break
			}
		}
		if !found {
			*modified = true
			existing.Volumes = append(existing.Volumes, required)
		}
	}
	for _, required := range required.AllowedFlexVolumes {
		found := false
		for _, curr := range existing.AllowedFlexVolumes {
			if equality.Semantic.DeepEqual(required.Driver, curr.Driver) {
				found = true
				break
			}
		}
		if !found {
			*modified = true
			existing.AllowedFlexVolumes = append(existing.AllowedFlexVolumes, required)
		}
	}
	setBool(modified, &existing.AllowHostNetwork, required.AllowHostNetwork)
	setBool(modified, &existing.AllowHostPorts, required.AllowHostPorts)
	setBool(modified, &existing.AllowHostPID, required.AllowHostPID)
	setBool(modified, &existing.AllowHostIPC, required.AllowHostIPC)
	setBoolPtr(modified, &existing.DefaultAllowPrivilegeEscalation, required.DefaultAllowPrivilegeEscalation)
	setBoolPtr(modified, &existing.AllowPrivilegeEscalation, required.AllowPrivilegeEscalation)
	if !equality.Semantic.DeepEqual(existing.SELinuxContext, required.SELinuxContext) {
		*modified = true
		existing.SELinuxContext = required.SELinuxContext
	}
	if !equality.Semantic.DeepEqual(existing.RunAsUser, required.RunAsUser) {
		*modified = true
		existing.RunAsUser = required.RunAsUser
	}
	if !equality.Semantic.DeepEqual(existing.FSGroup, required.FSGroup) {
		*modified = true
		existing.FSGroup = required.FSGroup
	}
	if !equality.Semantic.DeepEqual(existing.SupplementalGroups, required.SupplementalGroups) {
		*modified = true
		existing.SupplementalGroups = required.SupplementalGroups
	}
	setBool(modified, &existing.ReadOnlyRootFilesystem, required.ReadOnlyRootFilesystem)
	mergeStringSlice(modified, &existing.Users, required.Users)
	mergeStringSlice(modified, &existing.Groups, required.Groups)
	mergeStringSlice(modified, &existing.SeccompProfiles, required.SeccompProfiles)
	mergeStringSlice(modified, &existing.AllowedUnsafeSysctls, required.AllowedUnsafeSysctls)
	mergeStringSlice(modified, &existing.ForbiddenSysctls, required.ForbiddenSysctls)
}

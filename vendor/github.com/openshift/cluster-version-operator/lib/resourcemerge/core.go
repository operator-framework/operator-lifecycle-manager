package resourcemerge

import (
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
)

// EnsureConfigMap ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureConfigMap(modified *bool, existing *corev1.ConfigMap, required corev1.ConfigMap) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)

	mergeMap(modified, &existing.Data, required.Data)
}

// ensurePodTemplateSpec ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func ensurePodTemplateSpec(modified *bool, existing *corev1.PodTemplateSpec, required corev1.PodTemplateSpec) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)

	ensurePodSpec(modified, &existing.Spec, required.Spec)
}

func ensurePodSpec(modified *bool, existing *corev1.PodSpec, required corev1.PodSpec) {
	// any container we specify, we require.
	for _, required := range required.InitContainers {
		var existingCurr *corev1.Container
		for j, curr := range existing.InitContainers {
			if curr.Name == required.Name {
				existingCurr = &existing.InitContainers[j]
				break
			}
		}
		if existingCurr == nil {
			*modified = true
			existing.Containers = append(existing.InitContainers, corev1.Container{})
			existingCurr = &existing.InitContainers[len(existing.InitContainers)-1]
		}
		ensureContainer(modified, existingCurr, required)
	}

	for _, required := range required.Containers {
		var existingCurr *corev1.Container
		for j, curr := range existing.Containers {
			if curr.Name == required.Name {
				existingCurr = &existing.Containers[j]
				break
			}
		}
		if existingCurr == nil {
			*modified = true
			existing.Containers = append(existing.Containers, corev1.Container{})
			existingCurr = &existing.Containers[len(existing.Containers)-1]
		}
		ensureContainer(modified, existingCurr, required)
	}

	// any volume we specify, we require.
	for _, required := range required.Volumes {
		var existingCurr *corev1.Volume
		for j, curr := range existing.Volumes {
			if curr.Name == required.Name {
				existingCurr = &existing.Volumes[j]
				break
			}
		}
		if existingCurr == nil {
			*modified = true
			existing.Volumes = append(existing.Volumes, corev1.Volume{})
			existingCurr = &existing.Volumes[len(existing.Volumes)-1]
		}
		ensureVolume(modified, existingCurr, required)
	}

	if len(required.RestartPolicy) > 0 {
		if existing.RestartPolicy != required.RestartPolicy {
			*modified = true
			existing.RestartPolicy = required.RestartPolicy
		}
	}

	setStringIfSet(modified, &existing.ServiceAccountName, required.ServiceAccountName)
	setBool(modified, &existing.HostNetwork, required.HostNetwork)
	mergeMap(modified, &existing.NodeSelector, required.NodeSelector)
	ensurePodSecurityContextPtr(modified, &existing.SecurityContext, required.SecurityContext)
	ensureAffinityPtr(modified, &existing.Affinity, required.Affinity)
	ensureTolerations(modified, &existing.Tolerations, required.Tolerations)
	setStringIfSet(modified, &existing.PriorityClassName, required.PriorityClassName)
	setInt32Ptr(modified, &existing.Priority, required.Priority)
}

func ensureContainer(modified *bool, existing *corev1.Container, required corev1.Container) {
	setStringIfSet(modified, &existing.Name, required.Name)
	setStringIfSet(modified, &existing.Image, required.Image)

	// if you want modify the launch, you need to modify it in the config, not in the launch args
	setStringSlice(modified, &existing.Command, required.Command)
	setStringSlice(modified, &existing.Args, required.Args)
	ensureEnvVar(modified, &existing.Env, required.Env)
	ensureEnvFromSource(modified, &existing.EnvFrom, required.EnvFrom)
	setStringIfSet(modified, &existing.WorkingDir, required.WorkingDir)

	// any port we specify, we require
	for _, required := range required.Ports {
		var existingCurr *corev1.ContainerPort
		for j, curr := range existing.Ports {
			if curr.Name == required.Name {
				existingCurr = &existing.Ports[j]
				break
			}
		}
		if existingCurr == nil {
			*modified = true
			existing.Ports = append(existing.Ports, corev1.ContainerPort{})
			existingCurr = &existing.Ports[len(existing.Ports)-1]
		}
		ensureContainerPort(modified, existingCurr, required)
	}

	// any volume mount we specify, we require
	for _, required := range required.VolumeMounts {
		var existingCurr *corev1.VolumeMount
		for j, curr := range existing.VolumeMounts {
			if curr.Name == required.Name {
				existingCurr = &existing.VolumeMounts[j]
				break
			}
		}
		if existingCurr == nil {
			*modified = true
			existing.VolumeMounts = append(existing.VolumeMounts, corev1.VolumeMount{})
			existingCurr = &existing.VolumeMounts[len(existing.VolumeMounts)-1]
		}
		ensureVolumeMount(modified, existingCurr, required)
	}

	if required.LivenessProbe != nil {
		ensureProbePtr(modified, &existing.LivenessProbe, required.LivenessProbe)
	}
	if required.ReadinessProbe != nil {
		ensureProbePtr(modified, &existing.ReadinessProbe, required.ReadinessProbe)
	}

	// our security context should always win
	ensureSecurityContextPtr(modified, &existing.SecurityContext, required.SecurityContext)
}

func ensureEnvVar(modified *bool, existing *[]corev1.EnvVar, required []corev1.EnvVar) {
	if required == nil {
		return
	}
	if !equality.Semantic.DeepEqual(required, *existing) {
		*existing = required
		*modified = true
	}
}

func ensureEnvFromSource(modified *bool, existing *[]corev1.EnvFromSource, required []corev1.EnvFromSource) {
	if required == nil {
		return
	}
	if !equality.Semantic.DeepEqual(required, *existing) {
		*existing = required
		*modified = true
	}
}

func ensureProbePtr(modified *bool, existing **corev1.Probe, required *corev1.Probe) {
	// if we have no required, then we don't care what someone else has set
	if required == nil {
		return
	}
	if *existing == nil {
		*modified = true
		*existing = required
		return
	}
	ensureProbe(modified, *existing, *required)
}

func ensureProbe(modified *bool, existing *corev1.Probe, required corev1.Probe) {
	setInt32(modified, &existing.InitialDelaySeconds, required.InitialDelaySeconds)

	ensureProbeHandler(modified, &existing.Handler, required.Handler)
}

func ensureProbeHandler(modified *bool, existing *corev1.Handler, required corev1.Handler) {
	if !equality.Semantic.DeepEqual(required, *existing) {
		*modified = true
		*existing = required
	}
}

func ensureContainerPort(modified *bool, existing *corev1.ContainerPort, required corev1.ContainerPort) {
	if !equality.Semantic.DeepEqual(required, *existing) {
		*modified = true
		*existing = required
	}
}

func ensureVolumeMount(modified *bool, existing *corev1.VolumeMount, required corev1.VolumeMount) {
	if !equality.Semantic.DeepEqual(required, *existing) {
		*modified = true
		*existing = required
	}
}

func ensureVolume(modified *bool, existing *corev1.Volume, required corev1.Volume) {
	if !equality.Semantic.DeepEqual(required, *existing) {
		*modified = true
		*existing = required
	}
}

func ensureSecurityContextPtr(modified *bool, existing **corev1.SecurityContext, required *corev1.SecurityContext) {
	// if we have no required, then we don't care what someone else has set
	if required == nil {
		return
	}

	if *existing == nil {
		*modified = true
		*existing = required
		return
	}
	ensureSecurityContext(modified, *existing, *required)
}

func ensureSecurityContext(modified *bool, existing *corev1.SecurityContext, required corev1.SecurityContext) {
	ensureCapabilitiesPtr(modified, &existing.Capabilities, required.Capabilities)
	ensureSELinuxOptionsPtr(modified, &existing.SELinuxOptions, required.SELinuxOptions)
	setBoolPtr(modified, &existing.Privileged, required.Privileged)
	setInt64Ptr(modified, &existing.RunAsUser, required.RunAsUser)
	setBoolPtr(modified, &existing.RunAsNonRoot, required.RunAsNonRoot)
	setBoolPtr(modified, &existing.ReadOnlyRootFilesystem, required.ReadOnlyRootFilesystem)
	setBoolPtr(modified, &existing.AllowPrivilegeEscalation, required.AllowPrivilegeEscalation)
}

func ensureCapabilitiesPtr(modified *bool, existing **corev1.Capabilities, required *corev1.Capabilities) {
	// if we have no required, then we don't care what someone else has set
	if required == nil {
		return
	}

	if *existing == nil {
		*modified = true
		*existing = required
		return
	}
	ensureCapabilities(modified, *existing, *required)
}

func ensureCapabilities(modified *bool, existing *corev1.Capabilities, required corev1.Capabilities) {
	// any Add we specify, we require.
	for _, required := range required.Add {
		found := false
		for _, curr := range existing.Add {
			if equality.Semantic.DeepEqual(curr, required) {
				found = true
				break
			}
		}
		if !found {
			*modified = true
			existing.Add = append(existing.Add, required)
		}
	}

	// any Drop we specify, we require.
	for _, required := range required.Drop {
		found := false
		for _, curr := range existing.Drop {
			if equality.Semantic.DeepEqual(curr, required) {
				found = true
				break
			}
		}
		if !found {
			*modified = true
			existing.Drop = append(existing.Drop, required)
		}
	}
}

func setStringSliceIfSet(modified *bool, existing *[]string, required []string) {
	if required == nil {
		return
	}
	if !equality.Semantic.DeepEqual(required, *existing) {
		*existing = required
		*modified = true
	}
}

func setStringSlice(modified *bool, existing *[]string, required []string) {
	if !reflect.DeepEqual(required, *existing) {
		*existing = required
		*modified = true
	}
}

func mergeStringSlice(modified *bool, existing *[]string, required []string) {
	for _, required := range required {
		found := false
		for _, curr := range *existing {
			if required == curr {
				found = true
				break
			}
		}
		if !found {
			*modified = true
			*existing = append(*existing, required)
		}
	}
}

func ensureTolerations(modified *bool, existing *[]corev1.Toleration, required []corev1.Toleration) {
	for ridx := range required {
		found := false
		for eidx := range *existing {
			if required[ridx].Key == (*existing)[eidx].Key {
				found = true
				if !equality.Semantic.DeepEqual((*existing)[eidx], required[ridx]) {
					*modified = true
					(*existing)[eidx] = required[ridx]
				}
				break
			}
		}
		if !found {
			*modified = true
			*existing = append(*existing, required[ridx])
		}
	}
}

func ensureAffinityPtr(modified *bool, existing **corev1.Affinity, required *corev1.Affinity) {
	// if we have no required, then we don't care what someone else has set
	if required == nil {
		return
	}

	if *existing == nil {
		*modified = true
		*existing = required
		return
	}
	ensureAffinity(modified, *existing, *required)
}

func ensureAffinity(modified *bool, existing *corev1.Affinity, required corev1.Affinity) {
	if !equality.Semantic.DeepEqual(existing.NodeAffinity, required.NodeAffinity) {
		*modified = true
		(*existing).NodeAffinity = required.NodeAffinity
	}
	if !equality.Semantic.DeepEqual(existing.PodAffinity, required.PodAffinity) {
		*modified = true
		(*existing).PodAffinity = required.PodAffinity
	}
	if !equality.Semantic.DeepEqual(existing.PodAntiAffinity, required.PodAntiAffinity) {
		*modified = true
		(*existing).PodAntiAffinity = required.PodAntiAffinity
	}
}

func ensurePodSecurityContextPtr(modified *bool, existing **corev1.PodSecurityContext, required *corev1.PodSecurityContext) {
	// if we have no required, then we don't care what someone else has set
	if required == nil {
		return
	}

	if *existing == nil {
		*modified = true
		*existing = required
		return
	}
	ensurePodSecurityContext(modified, *existing, *required)
}

func ensurePodSecurityContext(modified *bool, existing *corev1.PodSecurityContext, required corev1.PodSecurityContext) {
	ensureSELinuxOptionsPtr(modified, &existing.SELinuxOptions, required.SELinuxOptions)
	setInt64Ptr(modified, &existing.RunAsUser, required.RunAsUser)
	setInt64Ptr(modified, &existing.RunAsGroup, required.RunAsGroup)
	setBoolPtr(modified, &existing.RunAsNonRoot, required.RunAsNonRoot)

	// any SupplementalGroups we specify, we require.
	for _, required := range required.SupplementalGroups {
		found := false
		for _, curr := range existing.SupplementalGroups {
			if curr == required {
				found = true
				break
			}
		}
		if !found {
			*modified = true
			existing.SupplementalGroups = append(existing.SupplementalGroups, required)
		}
	}

	setInt64Ptr(modified, &existing.FSGroup, required.FSGroup)

	// any SupplementalGroups we specify, we require.
	for _, required := range required.Sysctls {
		found := false
		for j, curr := range existing.Sysctls {
			if curr.Name == required.Name {
				found = true
				if curr.Value != required.Value {
					*modified = true
					existing.Sysctls[j] = required
				}
				break
			}
		}
		if !found {
			*modified = true
			existing.Sysctls = append(existing.Sysctls, required)
		}
	}
}

func ensureSELinuxOptionsPtr(modified *bool, existing **corev1.SELinuxOptions, required *corev1.SELinuxOptions) {
	// if we have no required, then we don't care what someone else has set
	if required == nil {
		return
	}

	if *existing == nil {
		*modified = true
		*existing = required
		return
	}
	ensureSELinuxOptions(modified, *existing, *required)
}

func ensureSELinuxOptions(modified *bool, existing *corev1.SELinuxOptions, required corev1.SELinuxOptions) {
	setStringIfSet(modified, &existing.User, required.User)
	setStringIfSet(modified, &existing.Role, required.Role)
	setStringIfSet(modified, &existing.Type, required.Type)
	setStringIfSet(modified, &existing.Level, required.Level)
}

func setBool(modified *bool, existing *bool, required bool) {
	if required != *existing {
		*existing = required
		*modified = true
	}
}

func setBoolPtr(modified *bool, existing **bool, required *bool) {
	// if we have no required, then we don't care what someone else has set
	if required == nil {
		return
	}

	if *existing == nil {
		*modified = true
		*existing = required
		return
	}
	setBool(modified, *existing, *required)
}

func setInt32(modified *bool, existing *int32, required int32) {
	if required != *existing {
		*existing = required
		*modified = true
	}
}

func setInt32Ptr(modified *bool, existing **int32, required *int32) {
	if *existing == nil || (required == nil && *existing != nil) {
		*modified = true
		*existing = required
		return
	}
	setInt32(modified, *existing, *required)
}

func setInt64(modified *bool, existing *int64, required int64) {
	if required != *existing {
		*existing = required
		*modified = true
	}
}

func setInt64Ptr(modified *bool, existing **int64, required *int64) {
	// if we have no required, then we don't care what someone else has set
	if required == nil {
		return
	}

	if *existing == nil {
		*modified = true
		*existing = required
		return
	}
	setInt64(modified, *existing, *required)
}

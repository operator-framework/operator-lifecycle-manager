package inject

import (
	"errors"
	"reflect"

	corev1 "k8s.io/api/core/v1"
)

// InjectEnvIntoDeployment injects the proxy env variables specified in
// proxyEnvVar into the container(s) of the given PodSpec.
//
// If any Container in PodSpec already defines an env variable of the same name
// as any of the proxy env variables then it
func InjectEnvIntoDeployment(podSpec *corev1.PodSpec, envVars []corev1.EnvVar) error {
	if podSpec == nil {
		return errors.New("no pod spec provided")
	}

	for i := range podSpec.Containers {
		container := &podSpec.Containers[i]
		container.Env = mergeEnvVars(container.Env, envVars)
	}

	return nil
}

func mergeEnvVars(containerEnvVars []corev1.EnvVar, newEnvVars []corev1.EnvVar) []corev1.EnvVar {
	// Build a map of environment variables.
	// newEnvVars always overrides containerEnvVars.
	mergedMap := map[string]corev1.EnvVar{}
	for _, envVar := range containerEnvVars {
		mergedMap[envVar.Name] = envVar
	}
	for _, envVar := range newEnvVars {
		mergedMap[envVar.Name] = envVar
	}

	// To keep things in the expected order, always put the
	// original environment variable names into the merged
	// output in place first.
	merged := make([]corev1.EnvVar, 0, len(mergedMap))
	for _, e := range containerEnvVars {
		envVar := mergedMap[e.Name]
		merged = append(merged, envVar)
		delete(mergedMap, e.Name)
	}

	// Then for any remaining newEnvVars (i.e. env vars
	// that weren't present in the containerEnvVars), add
	// them at the end in the order they were provided in
	// the subscription.
	for _, e := range newEnvVars {
		envVar, ok := mergedMap[e.Name]
		if !ok {
			continue
		}
		merged = append(merged, envVar)
	}

	return merged
}

// InjectVolumesIntoDeployment injects the provided Volumes
// into the container(s) of the given PodSpec.
//
// If any Container in PodSpec already defines a Volume of the same name
// as any of the provided Volumes then it will be overwritten.
func InjectVolumesIntoDeployment(podSpec *corev1.PodSpec, volumes []corev1.Volume) error {
	if podSpec == nil {
		return errors.New("no pod spec provided")
	}

	podSpec.Volumes = mergeVolumes(podSpec.Volumes, volumes)

	return nil
}

func mergeVolumes(podSpecVolumes []corev1.Volume, newVolumes []corev1.Volume) (merged []corev1.Volume) {
	merged = podSpecVolumes

	for _, newVolume := range newVolumes {
		existing, found := findVolume(podSpecVolumes, newVolume.Name)
		if found {
			*existing = newVolume
			continue
		}

		merged = append(merged, newVolume)
	}

	return
}

func findVolume(volumes []corev1.Volume, name string) (foundVolume *corev1.Volume, found bool) {
	for i := range volumes {
		if name == volumes[i].Name {
			// Environment variable names are case sensitive.
			found = true
			foundVolume = &volumes[i]

			break
		}
	}

	return
}

// InjectVolumeMountsIntoDeployment injects the provided VolumeMounts
// into the given PodSpec.
//
// If the PodSpec already defines a VolumeMount of the same name
// as any of the provided VolumeMounts then it will be overwritten.
func InjectVolumeMountsIntoDeployment(podSpec *corev1.PodSpec, volumeMounts []corev1.VolumeMount) error {
	if podSpec == nil {
		return errors.New("no pod spec provided")
	}

	for i := range podSpec.Containers {
		container := &podSpec.Containers[i]
		container.VolumeMounts = mergeVolumeMounts(container.VolumeMounts, volumeMounts)
	}

	return nil
}

func mergeVolumeMounts(containerVolumeMounts []corev1.VolumeMount, newVolumeMounts []corev1.VolumeMount) (merged []corev1.VolumeMount) {
	merged = containerVolumeMounts

	for _, newVolumeMount := range newVolumeMounts {
		existing, found := findVolumeMount(containerVolumeMounts, newVolumeMount.Name)
		if found {
			*existing = newVolumeMount
			continue
		}

		merged = append(merged, newVolumeMount)
	}

	return
}

func findVolumeMount(volumeMounts []corev1.VolumeMount, name string) (foundVolumeMount *corev1.VolumeMount, found bool) {
	for i := range volumeMounts {
		if name == volumeMounts[i].Name {
			// Environment variable names are case sensitive.
			found = true
			foundVolumeMount = &volumeMounts[i]

			break
		}
	}

	return
}

// InjectTolerationsIntoDeployment injects provided Tolerations
// into the given Pod Spec
//
// Tolerations will be appended to the existing once if it
// does not already exist
func InjectTolerationsIntoDeployment(podSpec *corev1.PodSpec, tolerations []corev1.Toleration) error {
	if podSpec == nil {
		return errors.New("no pod spec provided")
	}

	podSpec.Tolerations = mergeTolerations(podSpec.Tolerations, tolerations)
	return nil
}

func mergeTolerations(podTolerations []corev1.Toleration, newTolerations []corev1.Toleration) (mergedTolerations []corev1.Toleration) {
	mergedTolerations = podTolerations
	for _, newToleration := range newTolerations {
		_, found := findToleration(podTolerations, newToleration)
		if !found {
			mergedTolerations = append(mergedTolerations, newToleration)
		}
	}

	return
}

func findToleration(tolerations []corev1.Toleration, toleration corev1.Toleration) (foundToleration *corev1.Toleration, found bool) {
	for i := range tolerations {
		if reflect.DeepEqual(toleration, tolerations[i]) {
			found = true
			foundToleration = &toleration

			break
		}
	}

	return
}

// InjectResourcesIntoDeployment will inject provided Resources
// into given podSpec
//
// If podSpec already defines Resources, it will be overwritten
func InjectResourcesIntoDeployment(podSpec *corev1.PodSpec, resources *corev1.ResourceRequirements) error {
	if podSpec == nil {
		return errors.New("no pod spec provided")
	}

	if resources == nil {
		return nil
	}

	for i := range podSpec.Containers {
		container := &podSpec.Containers[i]
		container.Resources = *resources
	}

	return nil
}

// InjectNodeSelectorIntoDeployment injects the provided NodeSelector
// into the container(s) of the given PodSpec.
//
// If any Container in PodSpec already defines a NodeSelector it will
// be overwritten.
func InjectNodeSelectorIntoDeployment(podSpec *corev1.PodSpec, nodeSelector map[string]string) error {
	if podSpec == nil {
		return errors.New("no pod spec provided")
	}

	if nodeSelector != nil {
		podSpec.NodeSelector = nodeSelector
	}

	return nil
}

// OverrideDeploymentAffinity will override the corev1.Affinity defined in the Deployment
// with the given corev1.Affinity. Any nil top-level sub-attributes (e.g. NodeAffinity, PodAffinity, and PodAntiAffinity)
// will be ignored. Hint: to overwrite those top-level attributes, empty them out. I.e. use the empty/default object ({})
// e.g. NodeAffinity{}. In yaml:
//
//	affinity:
//	  nodeAffinity: {}
//	  podAffinity: {}
//	  podAntiAffinity: {}
//
// will completely remove the deployment podSpec.affinity and is equivalent to
// affinity: {}
func OverrideDeploymentAffinity(podSpec *corev1.PodSpec, affinity *corev1.Affinity) error {
	if podSpec == nil {
		return errors.New("no pod spec provided")
	}

	if affinity == nil {
		return nil
	}

	// if podSpec.Affinity is nil or empty/default then completely override podSpec.Affinity with overrides
	if podSpec.Affinity == nil || reflect.DeepEqual(podSpec.Affinity, &corev1.Affinity{}) {
		if reflect.DeepEqual(affinity, &corev1.Affinity{}) {
			podSpec.Affinity = nil
		} else {
			podSpec.Affinity = affinity
		}
		return nil
	}

	// if overriding affinity is empty/default then nil out podSpec.Affinity
	if reflect.DeepEqual(affinity, &corev1.Affinity{}) {
		podSpec.Affinity = nil
		return nil
	}

	// override podSpec.Affinity each attribute as necessary nilling out any default/empty overrides on the podSpec
	if affinity.NodeAffinity != nil {
		if reflect.DeepEqual(affinity.NodeAffinity, &corev1.NodeAffinity{}) {
			podSpec.Affinity.NodeAffinity = nil
		} else {
			podSpec.Affinity.NodeAffinity = affinity.NodeAffinity
		}
	}

	if affinity.PodAffinity != nil {
		if reflect.DeepEqual(affinity.PodAffinity, &corev1.PodAffinity{}) {
			podSpec.Affinity.PodAffinity = nil
		} else {
			podSpec.Affinity.PodAffinity = affinity.PodAffinity
		}
	}

	if affinity.PodAntiAffinity != nil {
		if reflect.DeepEqual(affinity.PodAntiAffinity, &corev1.PodAntiAffinity{}) {
			podSpec.Affinity = nil
		} else {
			podSpec.Affinity.PodAntiAffinity = affinity.PodAntiAffinity
		}
	}

	// special case: if after being overridden, podSpec is the same as default/empty then nil it out
	if reflect.DeepEqual(&corev1.Affinity{}, podSpec.Affinity) {
		podSpec.Affinity = nil
	}

	return nil
}

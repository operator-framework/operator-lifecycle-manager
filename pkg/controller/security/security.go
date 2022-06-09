package security

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/pointer"
)

const readOnlyRootFilesystem = false
const allowPrivilegeEscalation = false
const privileged = false
const runAsNonRoot = true

// See: https://github.com/operator-framework/operator-registry/blob/master/Dockerfile#L27
const runAsUser int64 = 1001

// ApplyPodSpecSecurity applies the standard security profile to a pod spec
func ApplyPodSpecSecurity(spec *corev1.PodSpec) {
	var containerSecurityContext = &corev1.SecurityContext{
		Privileged:               pointer.Bool(privileged),
		ReadOnlyRootFilesystem:   pointer.Bool(readOnlyRootFilesystem),
		AllowPrivilegeEscalation: pointer.Bool(allowPrivilegeEscalation),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}

	var podSecurityContext = &corev1.PodSecurityContext{
		RunAsNonRoot: pointer.Bool(runAsNonRoot),
		RunAsUser:    pointer.Int64(runAsUser),
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}

	spec.SecurityContext = podSecurityContext
	for idx := 0; idx < len(spec.Containers); idx++ {
		spec.Containers[idx].SecurityContext = containerSecurityContext
	}
	for idx := 0; idx < len(spec.InitContainers); idx++ {
		spec.InitContainers[idx].SecurityContext = containerSecurityContext
	}
}

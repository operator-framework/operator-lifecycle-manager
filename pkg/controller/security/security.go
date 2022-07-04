package security

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/pointer"
)

const readOnlyRootFilesystem = false
const allowPrivilegeEscalation = false
const privileged = false
const runAsNonRoot = true
const runAsUser = 1001

type PodSecurityOption = func(spec *corev1.PodSpec)

func WithRunAsUser() PodSecurityOption {
	return func(spec *corev1.PodSpec) {
		spec.SecurityContext.RunAsUser = pointer.Int64(runAsUser)
	}
}

// ApplyPodSpecSecurity applies the standard security profile to a pod spec
func ApplyPodSpecSecurity(spec *corev1.PodSpec, options ...PodSecurityOption) {
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

	for _, option := range options {
		option(spec)
	}
}

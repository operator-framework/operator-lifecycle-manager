package security

import corev1 "k8s.io/api/core/v1"

var readOnlyRootFilesystem = false
var allowPrivilegeEscalation = false
var runAsNonRoot = true

// See: https://github.com/operator-framework/operator-registry/blob/master/Dockerfile#L27
var runAsUser int64 = 1001

var containerSecurityContext = &corev1.SecurityContext{
	ReadOnlyRootFilesystem:   &readOnlyRootFilesystem,
	AllowPrivilegeEscalation: &allowPrivilegeEscalation,
	Capabilities: &corev1.Capabilities{
		Drop: []corev1.Capability{"ALL"},
	},
}

var podSecurityContext = &corev1.PodSecurityContext{
	RunAsNonRoot: &runAsNonRoot,
	RunAsUser:    &runAsUser,
	SeccompProfile: &corev1.SeccompProfile{
		Type: corev1.SeccompProfileTypeRuntimeDefault,
	},
}

// ApplyPodSpecSecurity applies the standard security profile to a pod spec
func ApplyPodSpecSecurity(spec *corev1.PodSpec) {
	spec.SecurityContext = podSecurityContext
	for idx := 0; idx < len(spec.Containers); idx++ {
		spec.Containers[idx].SecurityContext = containerSecurityContext
	}
	for idx := 0; idx < len(spec.InitContainers); idx++ {
		spec.InitContainers[idx].SecurityContext = containerSecurityContext
	}
}

package inject_test

import (
	"testing"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/olm/overrides/inject"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

var (
	defaultEnvVars = []corev1.EnvVar{
		corev1.EnvVar{
			Name:  "HTTP_PROXY",
			Value: "http://foo.com:8080",
		},
		corev1.EnvVar{
			Name:  "HTTPS_PROXY",
			Value: "https://foo.com:443",
		},
		corev1.EnvVar{
			Name:  "NO_PROXY",
			Value: "a.com,b.com",
		},
	}

	defaultVolumeMounts = []corev1.VolumeMount{
		corev1.VolumeMount{
			Name:      "foo",
			MountPath: "/bar",
		},
	}

	defaultVolumes = []corev1.Volume{
		corev1.Volume{
			Name:         "foo",
			VolumeSource: corev1.VolumeSource{},
		},
	}

	defaultTolerations = []corev1.Toleration{
		corev1.Toleration{
			Key:      "my-toleration-key",
			Effect:   corev1.TaintEffectNoSchedule,
			Value:    "my-toleration-value",
			Operator: corev1.TolerationOpEqual,
		},
	}

	defaultResources = corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("100m"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
	}

	defaultNodeSelector = map[string]string{
		"all":    "your",
		"base":   "are",
		"belong": "to",
		"us":     "",
	}
)

func TestInjectVolumeMountIntoDeployment(t *testing.T) {
	tests := []struct {
		name         string
		podSpec      *corev1.PodSpec
		volumeMounts []corev1.VolumeMount
		expected     *corev1.PodSpec
	}{
		{
			// The container does not define a VolumeMount and is injected with an empty list of VolumeMounts.
			// Expected: The container's VolumeMount list remains empty.
			name: "EmptyVolumeMounts",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{},
				},
			},
			volumeMounts: []corev1.VolumeMount{},
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{},
				},
			},
		},
		{
			// The container does not define a VolumeMount and is injected with a single VolumeMount.
			// Expected: The container contains the injected VolumeMount.
			name: "WithContainerHasNoVolumeMounts",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{},
				},
			},
			volumeMounts: defaultVolumeMounts,
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						VolumeMounts: defaultVolumeMounts,
					},
				},
			},
		},
		{
			// The container defines a single VolumeMount which is injected with an empty VolumeMount list.
			// Expected: The container's VolumeMount list is unchanged.
			name: "WithContainerHasVolumeMountsEmptyDefaults",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						VolumeMounts: defaultVolumeMounts,
					},
				},
			},
			volumeMounts: []corev1.VolumeMount{},
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						VolumeMounts: defaultVolumeMounts,
					},
				},
			},
		},
		{
			// The container defines a single VolumeMount and is injected with a new VolumeMount.
			// Expected: The container's VolumeMount list is updated to contain both VolumeMounts.
			name: "WithContainerHasNonOverlappingEnvVar",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						VolumeMounts: []corev1.VolumeMount{
							corev1.VolumeMount{
								Name:      "bar",
								MountPath: "/foo",
							},
						},
					},
				},
			},
			volumeMounts: defaultVolumeMounts,
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						VolumeMounts: []corev1.VolumeMount{
							corev1.VolumeMount{
								Name:      "bar",
								MountPath: "/foo",
							},
							corev1.VolumeMount{
								Name:      "foo",
								MountPath: "/bar",
							},
						},
					},
				},
			},
		},
		{
			// The container defines a single VolumeMount that has a name conflict with
			// a VolumeMount being injected.
			// Expected: The VolumeMount is overwritten.
			name: "WithContainerHasOverlappingVolumeMounts",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						VolumeMounts: []corev1.VolumeMount{
							corev1.VolumeMount{
								Name:      "foo",
								MountPath: "/barbar",
							},
						},
					},
				},
			},
			volumeMounts: defaultVolumeMounts,
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						VolumeMounts: []corev1.VolumeMount{
							corev1.VolumeMount{
								Name:      "foo",
								MountPath: "/bar",
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inject.InjectVolumeMountsIntoDeployment(tt.podSpec, tt.volumeMounts)

			podSpecWant := tt.expected
			podSpecGot := tt.podSpec

			assert.Equal(t, podSpecWant, podSpecGot)
		})
	}
}

func TestInjectVolumeIntoDeployment(t *testing.T) {
	tests := []struct {
		name     string
		podSpec  *corev1.PodSpec
		volumes  []corev1.Volume
		expected *corev1.PodSpec
	}{
		{
			// The PodSpec defines no Volumes and is injected with an empty list.
			// Expected: The PodSpec's VolumeMount list remains empty.
			name: "EmptyVolumeMounts",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{},
			},
			volumes: []corev1.Volume{},
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{},
			},
		},
		{
			// The PodSpec does not define any Volumes and is injected with a VolumeMount.
			// Expected: The PodSpec contains the Volume that was injected.
			name:    "WithContainerHasNoVolumeMounts",
			podSpec: &corev1.PodSpec{},
			volumes: defaultVolumes,
			expected: &corev1.PodSpec{
				Volumes: defaultVolumes,
			},
		},
		{
			// The PodSpec contains a single VolumeMount and is injected with an empty Volume list
			// Expected: The PodSpec's Volume list is unchanged.
			name: "WithContainerHasVolumeMountsEmptyDefaults",
			podSpec: &corev1.PodSpec{
				Volumes: defaultVolumes,
			},
			volumes: []corev1.Volume{},
			expected: &corev1.PodSpec{
				Volumes: defaultVolumes,
			},
		},
		{
			// The PodSpec defines single Volume and is injected with a new Volume.
			// Expected: The PodSpec contains both Volumes.
			name: "WithContainerHasNonOverlappingEnvVar",
			podSpec: &corev1.PodSpec{
				Volumes: []corev1.Volume{
					corev1.Volume{
						Name:         "bar",
						VolumeSource: corev1.VolumeSource{},
					},
				},
			},
			volumes: defaultVolumes,
			expected: &corev1.PodSpec{
				Volumes: []corev1.Volume{
					corev1.Volume{
						Name:         "bar",
						VolumeSource: corev1.VolumeSource{},
					},
					corev1.Volume{
						Name:         "foo",
						VolumeSource: corev1.VolumeSource{},
					},
				},
			},
		},
		{
			// The PodSpec defines a single Volume that is injected with a Volume that has a name conflict.
			// Expected: The existing Volume is overwritten.
			name: "WithContainerHasOverlappingVolumeMounts",
			podSpec: &corev1.PodSpec{
				Volumes: []corev1.Volume{
					corev1.Volume{
						Name: "foo",
					},
				},
			},
			volumes: defaultVolumes,
			expected: &corev1.PodSpec{
				Volumes: []corev1.Volume{
					corev1.Volume{
						Name:         "foo",
						VolumeSource: corev1.VolumeSource{},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inject.InjectVolumesIntoDeployment(tt.podSpec, tt.volumes)

			podSpecWant := tt.expected
			podSpecGot := tt.podSpec

			assert.Equal(t, podSpecWant, podSpecGot)
		})
	}
}

func TestInjectEnvIntoDeployment(t *testing.T) {
	tests := []struct {
		name     string
		podSpec  *corev1.PodSpec
		envVar   []corev1.EnvVar
		expected *corev1.PodSpec
	}{
		{
			// PodSpec has one container and `Env` is empty.
			// Expected: All env variable(s) specified are injected.
			name: "WithContainerHasNoEnvVar",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{},
				},
			},
			envVar: defaultEnvVars,
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Env: defaultEnvVars,
					},
				},
			},
		},

		{
			// PodSpec has one container and it has non overlapping env var(s).
			// Expected: existing non overlapping env vars are intact.
			name: "WithContainerHasNonOverlappingEnvVar",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Env: []corev1.EnvVar{
							corev1.EnvVar{
								Name:  "foo",
								Value: "foo_value",
							},
						},
					},
				},
			},
			envVar: defaultEnvVars,
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Env: append([]corev1.EnvVar{
							corev1.EnvVar{
								Name:  "foo",
								Value: "foo_value",
							},
						}, defaultEnvVars...),
					},
				},
			},
		},

		{
			// PodSpec has one container and it has overlapping env var.
			// Expected: overlapping env var is modified.
			name: "WithContainerHasOverlappingEnvVar",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Env: []corev1.EnvVar{
							corev1.EnvVar{
								Name:  "foo",
								Value: "foo_value",
							},
							corev1.EnvVar{
								Name:  "bar",
								Value: "bar_value",
							},
						},
					},
				},
			},
			envVar: []corev1.EnvVar{
				corev1.EnvVar{
					Name:  "foo",
					Value: "new_foo_value",
				},
				corev1.EnvVar{
					Name:  "bar",
					Value: "new_bar_value",
				},
			},
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Env: []corev1.EnvVar{
							corev1.EnvVar{
								Name:  "foo",
								Value: "new_foo_value",
							},
							corev1.EnvVar{
								Name:  "bar",
								Value: "new_bar_value",
							},
						},
					},
				},
			},
		},

		{
			// PodSpec has one container and it has overlapping env var which is being unset.
			// Expected: overlapping env var is modified.
			name: "WithContainerEnvVarBeingUnset",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Env: []corev1.EnvVar{
							corev1.EnvVar{
								Name:  "foo",
								Value: "foo_value",
							},
							corev1.EnvVar{
								Name:  "bar",
								Value: "bar_value",
							},
						},
					},
				},
			},
			envVar: []corev1.EnvVar{
				corev1.EnvVar{
					Name:  "bar",
					Value: "",
				},
			},
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Env: []corev1.EnvVar{
							corev1.EnvVar{
								Name:  "foo",
								Value: "foo_value",
							},
							corev1.EnvVar{
								Name:  "bar",
								Value: "",
							},
						},
					},
				},
			},
		},

		{
			// PodSpec has more than one container(s)
			// Expected: All container(s) should be updated as expected.
			name: "WithMultipleContainers",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{},
					corev1.Container{
						Env: []corev1.EnvVar{
							corev1.EnvVar{
								Name:  "foo",
								Value: "foo_value",
							},
						},
					},
					corev1.Container{
						Env: []corev1.EnvVar{
							corev1.EnvVar{
								Name:  "bar",
								Value: "bar_value",
							},
						},
					},
				},
			},
			envVar: defaultEnvVars,
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Env: defaultEnvVars,
					},
					corev1.Container{
						Env: append([]corev1.EnvVar{
							corev1.EnvVar{
								Name:  "foo",
								Value: "foo_value",
							},
						}, defaultEnvVars...),
					},
					corev1.Container{
						Env: append([]corev1.EnvVar{
							corev1.EnvVar{
								Name:  "bar",
								Value: "bar_value",
							},
						}, defaultEnvVars...),
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inject.InjectEnvIntoDeployment(tt.podSpec, tt.envVar)

			podSpecWant := tt.expected
			podSpecGot := tt.podSpec

			assert.Equal(t, podSpecWant, podSpecGot)
		})
	}
}

func TestInjectTolerationsIntoDeployment(t *testing.T) {
	tests := []struct {
		name        string
		podSpec     *corev1.PodSpec
		tolerations []corev1.Toleration
		expected    *corev1.PodSpec
	}{
		{
			// PodSpec has no tolerations and toleration config is empty
			// Expected: Tolerations will be empty
			name: "WithEmptyTolerations",
			podSpec: &corev1.PodSpec{
				Tolerations: []corev1.Toleration{},
			},
			tolerations: []corev1.Toleration{},
			expected: &corev1.PodSpec{
				Tolerations: []corev1.Toleration{},
			},
		},
		{
			// PodSpec has no tolerations and one toleration config given
			// Expected: Toleration will be appended
			name: "WithDeploymentHasNoTolerations",
			podSpec: &corev1.PodSpec{
				Tolerations: []corev1.Toleration{},
			},
			tolerations: defaultTolerations,
			expected: &corev1.PodSpec{
				Tolerations: defaultTolerations,
			},
		},
		{
			// PodSpec has one toleration and different toleration config given
			// Expected: Toleration will be appended
			name: "WithDeploymentHasOneNonOverlappingToleration",
			podSpec: &corev1.PodSpec{
				Tolerations: []corev1.Toleration{
					corev1.Toleration{
						Key:      "my-different-toleration-key",
						Operator: corev1.TolerationOpExists,
					},
				},
			},
			tolerations: defaultTolerations,
			expected: &corev1.PodSpec{
				Tolerations: append([]corev1.Toleration{
					corev1.Toleration{
						Key:      "my-different-toleration-key",
						Operator: corev1.TolerationOpExists,
					},
				}, defaultTolerations...),
			},
		},
		{
			// PodSpec has one toleration and same toleration config given
			// Expected: Toleration will not be appended
			name: "WithDeploymentHasOneOverlappingToleration",
			podSpec: &corev1.PodSpec{
				Tolerations: defaultTolerations,
			},
			tolerations: defaultTolerations,
			expected: &corev1.PodSpec{
				Tolerations: defaultTolerations,
			},
		},
		{
			// PodSpec has one toleration and 2 toleration config given with 1 overlapping
			// Expected: Non overlapping toleration will be appended
			name: "WithDeploymentHasOverlappingAndNonOverlappingTolerations",
			podSpec: &corev1.PodSpec{
				Tolerations: []corev1.Toleration{
					corev1.Toleration{
						Key:      "my-different-toleration-key",
						Operator: corev1.TolerationOpExists,
					},
				},
			},
			tolerations: append([]corev1.Toleration{
				corev1.Toleration{
					Key:      "my-different-toleration-key",
					Operator: corev1.TolerationOpExists,
				},
			}, defaultTolerations...),
			expected: &corev1.PodSpec{
				Tolerations: append([]corev1.Toleration{
					corev1.Toleration{
						Key:      "my-different-toleration-key",
						Operator: corev1.TolerationOpExists,
					},
				}, defaultTolerations...),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inject.InjectTolerationsIntoDeployment(tt.podSpec, tt.tolerations)

			podSpecWant := tt.expected
			podSpecGot := tt.podSpec

			assert.Equal(t, podSpecWant, podSpecGot)
		})
	}
}

func TestInjectResourcesIntoDeployment(t *testing.T) {
	tests := []struct {
		name      string
		podSpec   *corev1.PodSpec
		resources corev1.ResourceRequirements
		expected  *corev1.PodSpec
	}{
		{
			// PodSpec has one container and empty resources
			// Expected: Resources will remain empty
			name: "WithEmptyResources",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Resources: corev1.ResourceRequirements{},
					},
				},
			},
			resources: corev1.ResourceRequirements{},
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Resources: corev1.ResourceRequirements{},
					},
				},
			},
		},
		{
			// PodSpec has one container with empty resources and one resource config given
			// Expected: Resources will be appended
			name: "WithDeploymentHasNoResources",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Resources: corev1.ResourceRequirements{},
					},
				},
			},
			resources: defaultResources,
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Resources: defaultResources,
					},
				},
			},
		},
		{
			// PodSpec has one container with one resource and one resource config given
			// Expected: Resources will be overwritten
			// Here, overriding with empty resources
			name: "WithDeploymentHasResources",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Resources: defaultResources,
					},
				},
			},
			resources: corev1.ResourceRequirements{},
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Resources: corev1.ResourceRequirements{},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inject.InjectResourcesIntoDeployment(tt.podSpec, tt.resources)

			podSpecWant := tt.expected
			podSpecGot := tt.podSpec

			assert.Equal(t, podSpecWant, podSpecGot)
		})
	}
}

func TestInjectNodeSelectorIntoDeployment(t *testing.T) {
	tests := []struct {
		name         string
		podSpec      *corev1.PodSpec
		nodeSelector map[string]string
		expected     *corev1.PodSpec
	}{
		{
			// Nil PodSpec is injected with a nodeSelector
			// Expected: PodSpec is nil
			name:         "WithNilPodSpec",
			podSpec:      nil,
			nodeSelector: map[string]string{"foo": "bar"},
			expected:     nil,
		},
		{
			// PodSpec with no NodeSelector is injected with a nodeSelector
			// Expected: NodeSelector is empty
			name:         "WithEmptyNodeSelector",
			podSpec:      &corev1.PodSpec{},
			nodeSelector: map[string]string{"foo": "bar"},
			expected: &corev1.PodSpec{
				NodeSelector: map[string]string{"foo": "bar"},
			},
		},
		{
			// PodSpec with an existing NodeSelector is injected with a nodeSelector
			// Expected: Existing NodeSelector is overwritten
			name: "WithExistingNodeSelector",
			podSpec: &corev1.PodSpec{
				NodeSelector: defaultNodeSelector,
			},
			nodeSelector: map[string]string{"foo": "bar"},
			expected: &corev1.PodSpec{
				NodeSelector: map[string]string{"foo": "bar"},
			},
		},
		{
			// Existing PodSpec is left alone if nodeSelector is nil
			// Expected: PodSpec is not changed
			name: "WithNilNodeSelector",
			podSpec: &corev1.PodSpec{
				NodeSelector: defaultNodeSelector,
			},
			nodeSelector: nil,
			expected: &corev1.PodSpec{
				NodeSelector: defaultNodeSelector,
			},
		},
		{
			// Existing PodSpec is set to an empty map if the nodeSelector is an empty map
			// Expected: PodSpec nodeSelector is set to an empty map
			name: "WithEmptyNodeSelector",
			podSpec: &corev1.PodSpec{
				NodeSelector: defaultNodeSelector,
			},
			nodeSelector: map[string]string{},
			expected: &corev1.PodSpec{
				NodeSelector: map[string]string{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inject.InjectNodeSelectorIntoDeployment(tt.podSpec, tt.nodeSelector)

			podSpecWant := tt.expected
			podSpecGot := tt.podSpec

			assert.Equal(t, podSpecWant, podSpecGot)
		})
	}
}

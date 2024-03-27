package inject_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/olm/overrides/inject"
)

var (
	defaultEnvVars = []corev1.EnvVar{
		{
			Name:  "HTTP_PROXY",
			Value: "http://foo.com:8080",
		},
		{
			Name:  "HTTPS_PROXY",
			Value: "https://foo.com:443",
		},
		{
			Name:  "NO_PROXY",
			Value: "a.com,b.com",
		},
	}

	defaultEnvFromVars = []corev1.EnvFromSource{
		{
			Prefix: "test",
		},
		{
			ConfigMapRef: &corev1.ConfigMapEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "configmapForTest",
				},
			},
		},
		{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "secretForTest",
				},
			},
		},
	}

	defaultVolumeMounts = []corev1.VolumeMount{
		{
			Name:      "foo",
			MountPath: "/bar",
		},
	}

	defaultVolumes = []corev1.Volume{
		{
			Name:         "foo",
			VolumeSource: corev1.VolumeSource{},
		},
	}

	defaultTolerations = []corev1.Toleration{
		{
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
				Containers: []corev1.Container{},
			},
			volumeMounts: []corev1.VolumeMount{},
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{},
			},
		},
		{
			// The container does not define a VolumeMount and is injected with a single VolumeMount.
			// Expected: The container contains the injected VolumeMount.
			name: "WithContainerHasNoVolumeMounts",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{{}},
			},
			volumeMounts: defaultVolumeMounts,
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
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
					{
						VolumeMounts: defaultVolumeMounts,
					},
				},
			},
			volumeMounts: []corev1.VolumeMount{},
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
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
					{
						VolumeMounts: []corev1.VolumeMount{
							{
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
					{
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "bar",
								MountPath: "/foo",
							},
							{
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
					{
						VolumeMounts: []corev1.VolumeMount{
							{
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
					{
						VolumeMounts: []corev1.VolumeMount{
							{
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
					{
						Name:         "bar",
						VolumeSource: corev1.VolumeSource{},
					},
				},
			},
			volumes: defaultVolumes,
			expected: &corev1.PodSpec{
				Volumes: []corev1.Volume{
					{
						Name:         "bar",
						VolumeSource: corev1.VolumeSource{},
					},
					{
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
					{
						Name: "foo",
					},
				},
			},
			volumes: defaultVolumes,
			expected: &corev1.PodSpec{
				Volumes: []corev1.Volume{
					{
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
				Containers: []corev1.Container{{}},
			},
			envVar: defaultEnvVars,
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
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
					{
						Env: []corev1.EnvVar{
							{
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
					{
						Env: append([]corev1.EnvVar{
							{
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
					{
						Env: []corev1.EnvVar{
							{
								Name:  "foo",
								Value: "foo_value",
							},
							{
								Name:  "bar",
								Value: "bar_value",
							},
						},
					},
				},
			},
			envVar: []corev1.EnvVar{
				{
					Name:  "extra",
					Value: "extra_value",
				},
				{
					Name:  "foo",
					Value: "new_foo_value",
				},
				{
					Name:  "bar",
					Value: "new_bar_value",
				},
			},
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Env: []corev1.EnvVar{
							{
								Name:  "foo",
								Value: "new_foo_value",
							},
							{
								Name:  "bar",
								Value: "new_bar_value",
							},
							{
								Name:  "extra",
								Value: "extra_value",
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
					{
						Env: []corev1.EnvVar{
							{
								Name:  "foo",
								Value: "foo_value",
							},
							{
								Name:  "bar",
								Value: "bar_value",
							},
						},
					},
				},
			},
			envVar: []corev1.EnvVar{
				{
					Name:  "bar",
					Value: "",
				},
			},
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Env: []corev1.EnvVar{
							{
								Name:  "foo",
								Value: "foo_value",
							},
							{
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
					{},
					{
						Env: []corev1.EnvVar{
							{
								Name:  "foo",
								Value: "foo_value",
							},
						},
					},
					{
						Env: []corev1.EnvVar{
							{
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
					{
						Env: defaultEnvVars,
					},
					{
						Env: append([]corev1.EnvVar{
							{
								Name:  "foo",
								Value: "foo_value",
							},
						}, defaultEnvVars...),
					},
					{
						Env: append([]corev1.EnvVar{
							{
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

func TestInjectEnvFromIntoDeployment(t *testing.T) {
	tests := []struct {
		name       string
		podSpec    *corev1.PodSpec
		envFromVar []corev1.EnvFromSource
		expected   *corev1.PodSpec
	}{
		{
			// PodSpec has one container and `EnvFrom` is empty.
			// Expected: All env variable(s) specified are injected.
			name: "WithContainerHasNoEnvFromVar",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{},
				},
			},
			envFromVar: defaultEnvFromVars,
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						EnvFrom: defaultEnvFromVars,
					},
				},
			},
		},
		{
			// PodSpec has one container and it has overlapping envFrom var(s).
			// Expected: existing duplicate env vars won't be appended in the envFrom.
			name: "WithContainerHasOverlappingEnvFromVar",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						EnvFrom: []corev1.EnvFromSource{
							{
								Prefix: "test",
							},
							{
								ConfigMapRef: &corev1.ConfigMapEnvSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "configmapForTest",
									},
								},
							},
							{
								SecretRef: &corev1.SecretEnvSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "secretForTest",
									},
								},
							},
						},
					},
				},
			},
			envFromVar: defaultEnvFromVars,
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						EnvFrom: []corev1.EnvFromSource{
							{
								Prefix: "test",
							},
							{
								ConfigMapRef: &corev1.ConfigMapEnvSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "configmapForTest",
									},
								},
							},
							{
								SecretRef: &corev1.SecretEnvSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "secretForTest",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			// PodSpec has one container and it has non overlapping envFrom var(s).
			// Expected: existing non overlapping env vars are intact.
			name: "WithContainerHasNonOverlappingEnvFromVar",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						EnvFrom: []corev1.EnvFromSource{
							{
								Prefix: "foo",
							},
						},
					},
				},
			},
			envFromVar: defaultEnvFromVars,
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						EnvFrom: append([]corev1.EnvFromSource{
							{
								Prefix: "foo",
							},
						}, defaultEnvFromVars...),
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
					{},
					{
						EnvFrom: []corev1.EnvFromSource{
							{
								Prefix: "foo",
							},
						},
					},
					{
						EnvFrom: []corev1.EnvFromSource{
							{
								Prefix: "bar",
							},
						},
					},
				},
			},
			envFromVar: defaultEnvFromVars,
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						EnvFrom: defaultEnvFromVars,
					},
					{
						EnvFrom: append([]corev1.EnvFromSource{
							{
								Prefix: "foo",
							},
						}, defaultEnvFromVars...),
					},
					{
						EnvFrom: append([]corev1.EnvFromSource{
							{
								Prefix: "bar",
							},
						}, defaultEnvFromVars...),
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inject.InjectEnvFromIntoDeployment(tt.podSpec, tt.envFromVar)

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
					{
						Key:      "my-different-toleration-key",
						Operator: corev1.TolerationOpExists,
					},
				},
			},
			tolerations: defaultTolerations,
			expected: &corev1.PodSpec{
				Tolerations: append([]corev1.Toleration{
					{
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
					{
						Key:      "my-different-toleration-key",
						Operator: corev1.TolerationOpExists,
					},
				},
			},
			tolerations: append([]corev1.Toleration{
				{
					Key:      "my-different-toleration-key",
					Operator: corev1.TolerationOpExists,
				},
			}, defaultTolerations...),
			expected: &corev1.PodSpec{
				Tolerations: append([]corev1.Toleration{
					{
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
		resources *corev1.ResourceRequirements
		expected  *corev1.PodSpec
	}{
		{
			// PodSpec has one container and existing resources
			// Expected: PodSpec resources will remain untouched
			name: "WithNilResources",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Resources: defaultResources,
					},
				},
			},
			resources: nil,
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Resources: defaultResources,
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
					{
						Resources: corev1.ResourceRequirements{},
					},
				},
			},
			resources: &defaultResources,
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
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
					{
						Resources: defaultResources,
					},
				},
			},
			resources: &corev1.ResourceRequirements{},
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
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

func TestOverrideDeploymentAffinity(t *testing.T) {
	tests := []struct {
		name        string
		podSpec     *corev1.PodSpec
		affinity    *corev1.Affinity
		expected    *corev1.PodSpec
		expectedErr error
	}{
		{
			// Nil PodSpec is injected with an Affinity
			// Expected: PodSpec is nil
			// ExpectedErr: "no pod spec provided"
			name:        "WithNilPodSpec",
			podSpec:     nil,
			affinity:    &corev1.Affinity{},
			expected:    nil,
			expectedErr: fmt.Errorf("no pod spec provided"),
		},
		{
			// Affinity is overrides PodSpec with no Affinity
			// Expected: Affinity is defined in the PodSpec
			// ExpectedErr: nil
			name:    "WithPodSpecWithNoAffinity",
			podSpec: &corev1.PodSpec{},
			affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchFields: []corev1.NodeSelectorRequirement{
									{
										Key:      "key",
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{"val1", "val2"},
									},
								},
							},
						},
					},
				},
			},
			expected: &corev1.PodSpec{
				Affinity: &corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchFields: []corev1.NodeSelectorRequirement{
										{
											Key:      "key",
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"val1", "val2"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			// Affinity with NodeAffinity overrides PodSpec with NodeAffinity, PodAffinity, PodAntiAffinity
			// Expected: PodSpec Affinity has overridden NodeAffinity, but not PodAffinity, PodAntiAffinity
			// ExpectedErr: nil
			name: "OnlyOverrideNodeAffinity",
			podSpec: &corev1.PodSpec{
				Affinity: &corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchFields: []corev1.NodeSelectorRequirement{
										{
											Key:      "key",
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"val1", "val2"},
										},
									},
								},
							},
						},
					},
					PodAffinity: &corev1.PodAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								TopologyKey: "topKey",
								Namespaces:  []string{"ns1", "ns2"},
							},
						},
					},
					PodAntiAffinity: &corev1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								TopologyKey: "topKey2",
								Namespaces:  []string{"n3", "ns4"},
							},
						},
					},
				},
			},
			affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchFields: []corev1.NodeSelectorRequirement{
									{
										Key:      "anotherKey",
										Operator: corev1.NodeSelectorOpExists,
										Values:   []string{"val3"},
									},
								},
							},
						},
					},
				},
			},
			expected: &corev1.PodSpec{
				Affinity: &corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchFields: []corev1.NodeSelectorRequirement{
										{
											Key:      "anotherKey",
											Operator: corev1.NodeSelectorOpExists,
											Values:   []string{"val3"},
										},
									},
								},
							},
						},
					},
					PodAffinity: &corev1.PodAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								TopologyKey: "topKey",
								Namespaces:  []string{"ns1", "ns2"},
							},
						},
					},
					PodAntiAffinity: &corev1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								TopologyKey: "topKey2",
								Namespaces:  []string{"n3", "ns4"},
							},
						},
					},
				},
			},
		},
		{
			// Affinity with NodeAffinity, empty PodAffinity overrides PodSpec with NodeAffinity, PodAffinity, PodAntiAffinity
			// Expected: PodSpec Affinity has overridden NodeAffinity, PodAffinity, but not PodAntiAffinity
			// ExpectedErr: nil
			name: "OverrideNodeAffinityEmptyPodAffinity",
			podSpec: &corev1.PodSpec{
				Affinity: &corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchFields: []corev1.NodeSelectorRequirement{
										{
											Key:      "key",
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"val1", "val2"},
										},
									},
								},
							},
						},
					},
					PodAffinity: &corev1.PodAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								TopologyKey: "topKey",
								Namespaces:  []string{"ns1", "ns2"},
							},
						},
					},
					PodAntiAffinity: &corev1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								TopologyKey: "topKey2",
								Namespaces:  []string{"n3", "ns4"},
							},
						},
					},
				},
			},
			affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchFields: []corev1.NodeSelectorRequirement{
									{
										Key:      "anotherKey",
										Operator: corev1.NodeSelectorOpExists,
										Values:   []string{"val3"},
									},
								},
							},
						},
					},
				},
				PodAffinity: &corev1.PodAffinity{},
			},
			expected: &corev1.PodSpec{
				Affinity: &corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchFields: []corev1.NodeSelectorRequirement{
										{
											Key:      "anotherKey",
											Operator: corev1.NodeSelectorOpExists,
											Values:   []string{"val3"},
										},
									},
								},
							},
						},
					},
					PodAffinity: nil,
					PodAntiAffinity: &corev1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								TopologyKey: "topKey2",
								Namespaces:  []string{"n3", "ns4"},
							},
						},
					},
				},
			},
		},
		{
			// Affinity with NodeAffinity, PodAffinity overrides PodSpec with nil NodeAffinity, nil PodAffinity, PodAntiAffinity
			// Expected: PodSpec Affinity has overridden NodeAffinity, PodAffinity, but not PodAntiAffinity
			// ExpectedErr: nil
			name: "OverridesNilPodSpecAttributes",
			podSpec: &corev1.PodSpec{
				Affinity: &corev1.Affinity{
					PodAntiAffinity: &corev1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								TopologyKey: "topKey2",
								Namespaces:  []string{"n3", "ns4"},
							},
						},
					},
				},
			},
			affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchFields: []corev1.NodeSelectorRequirement{
									{
										Key:      "anotherKey",
										Operator: corev1.NodeSelectorOpExists,
										Values:   []string{"val3"},
									},
								},
							},
						},
					},
				},
				PodAffinity: &corev1.PodAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
						{
							TopologyKey: "topKey",
							Namespaces:  []string{"ns1", "ns2"},
						},
					},
				},
			},
			expected: &corev1.PodSpec{
				Affinity: &corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchFields: []corev1.NodeSelectorRequirement{
										{
											Key:      "anotherKey",
											Operator: corev1.NodeSelectorOpExists,
											Values:   []string{"val3"},
										},
									},
								},
							},
						},
					},
					PodAffinity: &corev1.PodAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								TopologyKey: "topKey",
								Namespaces:  []string{"ns1", "ns2"},
							},
						},
					},
					PodAntiAffinity: &corev1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								TopologyKey: "topKey2",
								Namespaces:  []string{"n3", "ns4"},
							},
						},
					},
				},
			},
		},
		{
			// Empty Affinity overrides PodSpec with NodeAffinity, PodAffinity, PodAntiAffinity
			// Expected: PodSpec Affinity is nil
			// ExpectedErr: nil
			name: "EmptyAffinity",
			podSpec: &corev1.PodSpec{
				Affinity: &corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchFields: []corev1.NodeSelectorRequirement{
										{
											Key:      "key",
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"val1", "val2"},
										},
									},
								},
							},
						},
					},
					PodAffinity: &corev1.PodAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								TopologyKey: "topKey",
								Namespaces:  []string{"ns1", "ns2"},
							},
						},
					},
					PodAntiAffinity: &corev1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								TopologyKey: "topKey2",
								Namespaces:  []string{"n3", "ns4"},
							},
						},
					},
				},
			},
			affinity: &corev1.Affinity{},
			expected: &corev1.PodSpec{
				Affinity: nil,
			},
		},
		{
			// Empty Affinity NodeAffinity, and PodAffinity
			// overrides PodSpec with NodeAffinity, and PodAffinity
			// Expected: PodSpec Affinity is nil
			// ExpectedErr: nil
			name: "Nil/EmptyAffinityAreEquivalent",
			podSpec: &corev1.PodSpec{
				Affinity: &corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchFields: []corev1.NodeSelectorRequirement{
										{
											Key:      "key",
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"val1", "val2"},
										},
									},
								},
							},
						},
					},
					PodAffinity: &corev1.PodAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								TopologyKey: "topKey",
								Namespaces:  []string{"ns1", "ns2"},
							},
						},
					},
				},
			},
			affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{},
				PodAffinity:  &corev1.PodAffinity{},
			},
			expected: &corev1.PodSpec{
				Affinity: nil,
			},
		},
		{
			// Nil Affinity overrides nothing PodSpec
			// Expected: PodSpec unaffected
			// ExpectedErr: nil
			name: "EmptyAffinity",
			podSpec: &corev1.PodSpec{
				Affinity: &corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchFields: []corev1.NodeSelectorRequirement{
										{
											Key:      "key",
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"val1", "val2"},
										},
									},
								},
							},
						},
					},
					PodAffinity: &corev1.PodAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								TopologyKey: "topKey",
								Namespaces:  []string{"ns1", "ns2"},
							},
						},
					},
					PodAntiAffinity: &corev1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								TopologyKey: "topKey2",
								Namespaces:  []string{"n3", "ns4"},
							},
						},
					},
				},
			},
			affinity: nil,
			expected: &corev1.PodSpec{
				Affinity: &corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchFields: []corev1.NodeSelectorRequirement{
										{
											Key:      "key",
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"val1", "val2"},
										},
									},
								},
							},
						},
					},
					PodAffinity: &corev1.PodAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								TopologyKey: "topKey",
								Namespaces:  []string{"ns1", "ns2"},
							},
						},
					},
					PodAntiAffinity: &corev1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								TopologyKey: "topKey2",
								Namespaces:  []string{"n3", "ns4"},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := inject.OverrideDeploymentAffinity(tt.podSpec, tt.affinity)

			podSpecWant := tt.expected
			podSpecGot := tt.podSpec

			assert.Equal(t, tt.expectedErr, err)
			assert.Equal(t, podSpecWant, podSpecGot)
		})
	}
}

func TestAffinityAPIChanges(t *testing.T) {
	value := reflect.ValueOf(corev1.Affinity{})
	assert.Equal(t, 3, value.NumField(), "It seems the corev1.Affinity API has changed. Please revisit the inject.OverrideDeploymentAffinity implementation")
}

func TestInjectAnnotationsIntoDeployment(t *testing.T) {
	tests := []struct {
		name        string
		deployment  *appsv1.Deployment
		annotations map[string]string
		expected    *appsv1.Deployment
	}{
		{
			// Nil Deployment is injected with annotations
			// Expected: Deployment is nil
			name:        "WithNilDeployment",
			deployment:  nil,
			annotations: map[string]string{"foo": "bar"},
			expected:    nil, // raises an error
		},
		{
			// Deployment with no Annotations is injected with annotations
			// Expected: Annotations is empty
			name:        "WithEmptyAnnotations",
			deployment:  &appsv1.Deployment{},
			annotations: map[string]string{"foo": "bar"},
			expected: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"foo": "bar"},
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								"foo": "bar",
							},
						},
					},
				},
			},
		},
		{
			// Deployment with existing Annotations is injected with annotations
			// Expected: Existing Annotations are not overwritten
			name: "WithExistingAnnotations",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"common": "default-deploy-annotation",
						"deploy": "default-deploy-annotation",
					},
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								"common": "default-pod-annotation",
								"pod":    "default-pod-annotation",
							},
						},
					},
				},
			},
			annotations: map[string]string{
				"common": "override-annotation",
				"deploy": "override-annotation",
				"pod":    "override-annotation",
				"foo":    "bar",
			},
			expected: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						// no overrides
						"common": "default-deploy-annotation",
						"deploy": "default-deploy-annotation",
						// there was no default for "pod" on the deployment
						"pod": "override-annotation",
						"foo": "bar",
					},
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								// no overrides
								"common": "default-pod-annotation",
								"pod":    "default-pod-annotation",
								// there was no default for "deploy" on the pod
								"deploy": "override-annotation",
								"foo":    "bar",
							},
						},
					},
				},
			},
		},
		{
			// Existing Deployment is left alone if annotations is nil
			// Expected: Deployment is not changed
			name: "WithNilAnnotations",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"deploy": "default-annotation",
					},
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								"pod": "default-annotation",
							},
						},
					},
				},
			},
			annotations: nil,
			expected: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"deploy": "default-annotation",
					},
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								"pod": "default-annotation",
							},
						},
					},
				},
			},
		},
		{
			// Existing Deployment retains its annotations if the annotations is an empty map
			// Expected: Deployment annotations are not changed
			name: "WithEmptyAnnotations",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"deploy": "default-annotation",
					},
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								"pod": "default-annotation",
							},
						},
					},
				},
			},
			annotations: map[string]string{},
			expected: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"deploy": "default-annotation",
					},
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								"pod": "default-annotation",
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inject.InjectAnnotationsIntoDeployment(tt.deployment, tt.annotations)

			podSpecWant := tt.expected
			podSpecGot := tt.deployment

			assert.Equal(t, podSpecWant, podSpecGot)
		})
	}
}

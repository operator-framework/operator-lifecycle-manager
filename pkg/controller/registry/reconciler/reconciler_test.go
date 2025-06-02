package reconciler

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/google/go-cmp/cmp"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/image"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
)

const workloadUserID = 1001
const defaultPodSecurityConfig = v1alpha1.Restricted

func TestPodMemoryTarget(t *testing.T) {
	q := resource.MustParse("5Mi")
	var testCases = []struct {
		name     string
		input    *v1alpha1.CatalogSource
		expected *corev1.Pod
	}{
		{
			name: "no memory target set",
			input: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
			},
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-",
					Namespace:    "testns",
					Labels:       map[string]string{"olm.pod-spec-hash": "8SbHWyYfjbRT8lLcfdZ5ofXNdC1GE6ayztILTF", "olm.managed": "true"},
					Annotations:  map[string]string{"cluster-autoscaler.kubernetes.io/safe-to-evict": "true"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "name",
							Image: "image",
							Ports: []corev1.ContainerPort{{Name: "grpc", ContainerPort: 50051}},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								InitialDelaySeconds: 0,
								TimeoutSeconds:      5,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								InitialDelaySeconds: 0,
								TimeoutSeconds:      5,
							},
							StartupProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								FailureThreshold: 10,
								PeriodSeconds:    10,
								TimeoutSeconds:   5,
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("50Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								ReadOnlyRootFilesystem: ptr.To(false),
							},
							ImagePullPolicy:          image.InferImagePullPolicy("image"),
							TerminationMessagePolicy: "FallbackToLogsOnError",
						},
					},
					NodeSelector:       map[string]string{"kubernetes.io/os": "linux"},
					ServiceAccountName: "service-account",
				},
			},
		},
		{
			name: "memory target set",
			input: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						MemoryTarget: &q,
					},
				},
			},
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-",
					Namespace:    "testns",
					Labels:       map[string]string{"olm.pod-spec-hash": "3DSBhZZIiOl5YIjTsZy9aRyFIXeDR8mZCGAcYA", "olm.managed": "true"},
					Annotations:  map[string]string{"cluster-autoscaler.kubernetes.io/safe-to-evict": "true"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "name",
							Image: "image",
							Ports: []corev1.ContainerPort{{Name: "grpc", ContainerPort: 50051}},
							Env:   []corev1.EnvVar{{Name: "GOMEMLIMIT", Value: "5MiB"}},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								InitialDelaySeconds: 0,
								TimeoutSeconds:      5,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								InitialDelaySeconds: 0,
								TimeoutSeconds:      5,
							},
							StartupProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								FailureThreshold: 10,
								PeriodSeconds:    10,
								TimeoutSeconds:   5,
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("5Mi"),
								},
								Limits: corev1.ResourceList{},
							},
							SecurityContext: &corev1.SecurityContext{
								ReadOnlyRootFilesystem: ptr.To(false),
							},
							ImagePullPolicy:          image.InferImagePullPolicy("image"),
							TerminationMessagePolicy: "FallbackToLogsOnError",
						},
					},
					NodeSelector:       map[string]string{"kubernetes.io/os": "linux"},
					ServiceAccountName: "service-account",
				},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pod, err := Pod(testCase.input, "name", "opmImage", "utilImage", "image", serviceAccount("", "service-account"), map[string]string{}, map[string]string{}, int32(0), int32(0), int64(workloadUserID), v1alpha1.Legacy)
			require.NoError(t, err)
			if diff := cmp.Diff(pod, testCase.expected); diff != "" {
				t.Errorf("got incorrect pod: %v", diff)
			}
		})
	}
}

func serviceAccount(namespace, name string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
}

func TestPodExtractContent(t *testing.T) {
	var testCases = []struct {
		name                  string
		input                 *v1alpha1.CatalogSource
		securityContextConfig v1alpha1.SecurityConfig
		expected              *corev1.Pod
	}{
		{
			name: "content extraction not requested - legacy security context config",
			input: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
			},
			securityContextConfig: v1alpha1.Legacy,
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-",
					Namespace:    "testns",
					Labels:       map[string]string{"olm.pod-spec-hash": "8SbHWyYfjbRT8lLcfdZ5ofXNdC1GE6ayztILTF", "olm.managed": "true"},
					Annotations:  map[string]string{"cluster-autoscaler.kubernetes.io/safe-to-evict": "true"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "name",
							Image: "image",
							Ports: []corev1.ContainerPort{{Name: "grpc", ContainerPort: 50051}},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								InitialDelaySeconds: 0,
								TimeoutSeconds:      5,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								InitialDelaySeconds: 0,
								TimeoutSeconds:      5,
							},
							StartupProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								FailureThreshold: 10,
								PeriodSeconds:    10,
								TimeoutSeconds:   5,
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("50Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								ReadOnlyRootFilesystem: ptr.To(false),
							},
							ImagePullPolicy:          image.InferImagePullPolicy("image"),
							TerminationMessagePolicy: "FallbackToLogsOnError",
						},
					},
					NodeSelector:       map[string]string{"kubernetes.io/os": "linux"},
					ServiceAccountName: "service-account",
				},
			},
		},
		{
			name: "content extraction expected - legacy security context config",
			input: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						ExtractContent: &v1alpha1.ExtractContentConfig{
							CacheDir:   "/tmp/cache",
							CatalogDir: "/catalog",
						},
					},
				},
			},
			securityContextConfig: v1alpha1.Legacy,
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-",
					Namespace:    "testns",
					Labels:       map[string]string{"olm.pod-spec-hash": "5MSUJs07MqD3fl9supmPaRNxD9N6tK8Bjo4OFl", "olm.managed": "true"},
					Annotations:  map[string]string{"cluster-autoscaler.kubernetes.io/safe-to-evict": "true"},
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name:         "utilities",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
						{
							Name:         "catalog-content",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name:                     "extract-utilities",
							Image:                    "utilImage",
							Command:                  []string{"cp"},
							Args:                     []string{"/bin/copy-content", "/utilities/copy-content"},
							VolumeMounts:             []corev1.VolumeMount{{Name: "utilities", MountPath: "/utilities"}},
							TerminationMessagePolicy: "FallbackToLogsOnError",
						},
						{
							Name:            "extract-content",
							Image:           "image",
							ImagePullPolicy: image.InferImagePullPolicy("image"),
							Command:         []string{"/utilities/copy-content"},
							Args: []string{
								"--catalog.from=/catalog",
								"--catalog.to=/extracted-catalog/catalog",
								"--cache.from=/tmp/cache",
								"--cache.to=/extracted-catalog/cache",
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "utilities", MountPath: "/utilities"},
								{Name: "catalog-content", MountPath: "/extracted-catalog"},
							},
							TerminationMessagePolicy: "FallbackToLogsOnError",
						},
					},
					Containers: []corev1.Container{
						{
							Name:    "name",
							Image:   "opmImage",
							Command: []string{"/bin/opm"},
							Args:    []string{"serve", "/extracted-catalog/catalog", "--cache-dir=/extracted-catalog/cache"},
							Ports:   []corev1.ContainerPort{{Name: "grpc", ContainerPort: 50051}},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								InitialDelaySeconds: 0,
								TimeoutSeconds:      5,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								InitialDelaySeconds: 0,
								TimeoutSeconds:      5,
							},
							StartupProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								FailureThreshold: 10,
								PeriodSeconds:    10,
								TimeoutSeconds:   5,
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("50Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								ReadOnlyRootFilesystem: ptr.To(false),
							},
							ImagePullPolicy:          image.InferImagePullPolicy("image"),
							TerminationMessagePolicy: "FallbackToLogsOnError",
							VolumeMounts:             []corev1.VolumeMount{{Name: "catalog-content", MountPath: "/extracted-catalog"}},
						},
					},
					NodeSelector:       map[string]string{"kubernetes.io/os": "linux"},
					ServiceAccountName: "service-account",
				},
			},
		},
		{
			name: "content extraction expected - legacy security context config, no catalog cache dir",
			input: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						ExtractContent: &v1alpha1.ExtractContentConfig{
							CatalogDir: "/catalog",
						},
					},
				},
			},
			securityContextConfig: v1alpha1.Legacy,
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-",
					Namespace:    "testns",
					Labels:       map[string]string{"olm.pod-spec-hash": "b4ns9MTvaRBYOarmuFe6PLYK0r2kxj5Vo06WTU", "olm.managed": "true"},
					Annotations:  map[string]string{"cluster-autoscaler.kubernetes.io/safe-to-evict": "true"},
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name:         "utilities",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
						{
							Name:         "catalog-content",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name:                     "extract-utilities",
							Image:                    "utilImage",
							Command:                  []string{"cp"},
							Args:                     []string{"/bin/copy-content", "/utilities/copy-content"},
							VolumeMounts:             []corev1.VolumeMount{{Name: "utilities", MountPath: "/utilities"}},
							TerminationMessagePolicy: "FallbackToLogsOnError",
						},
						{
							Name:            "extract-content",
							Image:           "image",
							ImagePullPolicy: image.InferImagePullPolicy("image"),
							Command:         []string{"/utilities/copy-content"},
							Args: []string{
								"--catalog.from=/catalog",
								"--catalog.to=/extracted-catalog/catalog",
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "utilities", MountPath: "/utilities"},
								{Name: "catalog-content", MountPath: "/extracted-catalog"},
							},
							TerminationMessagePolicy: "FallbackToLogsOnError",
						},
					},
					Containers: []corev1.Container{
						{
							Name:    "name",
							Image:   "opmImage",
							Command: []string{"/bin/opm"},
							Args:    []string{"serve", "/extracted-catalog/catalog"},
							Ports:   []corev1.ContainerPort{{Name: "grpc", ContainerPort: 50051}},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								InitialDelaySeconds: 0,
								TimeoutSeconds:      5,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								InitialDelaySeconds: 0,
								TimeoutSeconds:      5,
							},
							StartupProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								FailureThreshold: 10,
								PeriodSeconds:    10,
								TimeoutSeconds:   5,
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("50Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								ReadOnlyRootFilesystem: ptr.To(false),
							},
							ImagePullPolicy:          image.InferImagePullPolicy("image"),
							TerminationMessagePolicy: "FallbackToLogsOnError",
							VolumeMounts:             []corev1.VolumeMount{{Name: "catalog-content", MountPath: "/extracted-catalog"}},
						},
					},
					NodeSelector:       map[string]string{"kubernetes.io/os": "linux"},
					ServiceAccountName: "service-account",
				},
			},
		},
		{
			name: "content extraction not requested - restricted security context config",
			input: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
			},
			securityContextConfig: v1alpha1.Restricted,
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-",
					Namespace:    "testns",
					Labels:       map[string]string{"olm.pod-spec-hash": "3sDLk8MMNptrqUfdnruY2gUi1g8O4wpMWC6Q52", "olm.managed": "true"},
					Annotations:  map[string]string{"cluster-autoscaler.kubernetes.io/safe-to-evict": "true"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "name",
							Image: "image",
							Ports: []corev1.ContainerPort{{Name: "grpc", ContainerPort: 50051}},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								InitialDelaySeconds: 0,
								TimeoutSeconds:      5,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								InitialDelaySeconds: 0,
								TimeoutSeconds:      5,
							},
							StartupProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								FailureThreshold: 10,
								PeriodSeconds:    10,
								TimeoutSeconds:   5,
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("50Mi"),
								},
							},
							ImagePullPolicy: image.InferImagePullPolicy("image"),
							SecurityContext: &corev1.SecurityContext{
								Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
								AllowPrivilegeEscalation: ptr.To(false),
								ReadOnlyRootFilesystem:   ptr.To(false),
							},
							TerminationMessagePolicy: "FallbackToLogsOnError",
						},
					},
					NodeSelector: map[string]string{"kubernetes.io/os": "linux"},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:      ptr.To(int64(workloadUserID)),
						RunAsNonRoot:   ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					ServiceAccountName: "service-account",
				},
			},
		},
		{
			name: "content extraction expected - restricted security context config",
			input: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						ExtractContent: &v1alpha1.ExtractContentConfig{
							CacheDir:   "/tmp/cache",
							CatalogDir: "/catalog",
						},
					},
				},
			},
			securityContextConfig: v1alpha1.Restricted,
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-",
					Namespace:    "testns",
					Labels:       map[string]string{"olm.pod-spec-hash": "1X4YqbfXuc9SB9ztW03WNOyanr9aIhKfijeBHH", "olm.managed": "true"},
					Annotations:  map[string]string{"cluster-autoscaler.kubernetes.io/safe-to-evict": "true"},
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name:         "utilities",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
						{
							Name:         "catalog-content",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name:    "extract-utilities",
							Image:   "utilImage",
							Command: []string{"cp"},
							Args:    []string{"/bin/copy-content", "/utilities/copy-content"},
							SecurityContext: &corev1.SecurityContext{
								Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
								AllowPrivilegeEscalation: ptr.To(false),
							},
							VolumeMounts:             []corev1.VolumeMount{{Name: "utilities", MountPath: "/utilities"}},
							TerminationMessagePolicy: "FallbackToLogsOnError",
						},
						{
							Name:            "extract-content",
							Image:           "image",
							ImagePullPolicy: image.InferImagePullPolicy("image"),
							Command:         []string{"/utilities/copy-content"},
							Args: []string{
								"--catalog.from=/catalog",
								"--catalog.to=/extracted-catalog/catalog",
								"--cache.from=/tmp/cache",
								"--cache.to=/extracted-catalog/cache",
							},
							SecurityContext: &corev1.SecurityContext{
								Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
								AllowPrivilegeEscalation: ptr.To(false),
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "utilities", MountPath: "/utilities"},
								{Name: "catalog-content", MountPath: "/extracted-catalog"},
							},
							TerminationMessagePolicy: "FallbackToLogsOnError",
						},
					},
					Containers: []corev1.Container{
						{
							Name:    "name",
							Image:   "opmImage",
							Command: []string{"/bin/opm"},
							Args:    []string{"serve", "/extracted-catalog/catalog", "--cache-dir=/extracted-catalog/cache"},
							Ports:   []corev1.ContainerPort{{Name: "grpc", ContainerPort: 50051}},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								InitialDelaySeconds: 0,
								TimeoutSeconds:      5,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								InitialDelaySeconds: 0,
								TimeoutSeconds:      5,
							},
							StartupProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								FailureThreshold: 10,
								PeriodSeconds:    10,
								TimeoutSeconds:   5,
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("50Mi"),
								},
							},
							ImagePullPolicy: image.InferImagePullPolicy("image"),
							SecurityContext: &corev1.SecurityContext{
								Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
								AllowPrivilegeEscalation: ptr.To(false),
								ReadOnlyRootFilesystem:   ptr.To(false),
							},
							TerminationMessagePolicy: "FallbackToLogsOnError",
							VolumeMounts:             []corev1.VolumeMount{{Name: "catalog-content", MountPath: "/extracted-catalog"}},
						},
					},
					NodeSelector: map[string]string{"kubernetes.io/os": "linux"},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:      ptr.To(int64(workloadUserID)),
						RunAsNonRoot:   ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					ServiceAccountName: "service-account",
				},
			},
		},
		{
			name: "content extraction expected - restricted security context config, no catalog cache dir",
			input: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						ExtractContent: &v1alpha1.ExtractContentConfig{
							CatalogDir: "/catalog",
						},
					},
				},
			},
			securityContextConfig: v1alpha1.Restricted,
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-",
					Namespace:    "testns",
					Labels:       map[string]string{"olm.pod-spec-hash": "cO4moUo3vz6jZlcoBcxY4BB8o8a4E7m5GXCzI", "olm.managed": "true"},
					Annotations:  map[string]string{"cluster-autoscaler.kubernetes.io/safe-to-evict": "true"},
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name:         "utilities",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
						{
							Name:         "catalog-content",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name:    "extract-utilities",
							Image:   "utilImage",
							Command: []string{"cp"},
							Args:    []string{"/bin/copy-content", "/utilities/copy-content"},
							SecurityContext: &corev1.SecurityContext{
								Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
								AllowPrivilegeEscalation: ptr.To(false),
							},
							VolumeMounts:             []corev1.VolumeMount{{Name: "utilities", MountPath: "/utilities"}},
							TerminationMessagePolicy: "FallbackToLogsOnError",
						},
						{
							Name:            "extract-content",
							Image:           "image",
							ImagePullPolicy: image.InferImagePullPolicy("image"),
							Command:         []string{"/utilities/copy-content"},
							Args: []string{
								"--catalog.from=/catalog",
								"--catalog.to=/extracted-catalog/catalog",
							},
							SecurityContext: &corev1.SecurityContext{
								Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
								AllowPrivilegeEscalation: ptr.To(false),
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "utilities", MountPath: "/utilities"},
								{Name: "catalog-content", MountPath: "/extracted-catalog"},
							},
							TerminationMessagePolicy: "FallbackToLogsOnError",
						},
					},
					Containers: []corev1.Container{
						{
							Name:    "name",
							Image:   "opmImage",
							Command: []string{"/bin/opm"},
							Args:    []string{"serve", "/extracted-catalog/catalog"},
							Ports:   []corev1.ContainerPort{{Name: "grpc", ContainerPort: 50051}},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								InitialDelaySeconds: 0,
								TimeoutSeconds:      5,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								InitialDelaySeconds: 0,
								TimeoutSeconds:      5,
							},
							StartupProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"grpc_health_probe", "-addr=:50051"},
									},
								},
								FailureThreshold: 10,
								PeriodSeconds:    10,
								TimeoutSeconds:   5,
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("50Mi"),
								},
							},
							ImagePullPolicy: image.InferImagePullPolicy("image"),
							SecurityContext: &corev1.SecurityContext{
								Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
								AllowPrivilegeEscalation: ptr.To(false),
								ReadOnlyRootFilesystem:   ptr.To(false),
							},
							TerminationMessagePolicy: "FallbackToLogsOnError",
							VolumeMounts:             []corev1.VolumeMount{{Name: "catalog-content", MountPath: "/extracted-catalog"}},
						},
					},
					NodeSelector: map[string]string{"kubernetes.io/os": "linux"},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:      ptr.To(int64(workloadUserID)),
						RunAsNonRoot:   ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					ServiceAccountName: "service-account",
				},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pod, err := Pod(testCase.input, "name", "opmImage", "utilImage", "image", serviceAccount("", "service-account"), map[string]string{}, map[string]string{}, int32(0), int32(0), int64(workloadUserID), testCase.securityContextConfig)
			require.NoError(t, err)
			if diff := cmp.Diff(testCase.expected, pod); diff != "" {
				t.Errorf("got incorrect pod: %v", diff)
			}
		})
	}
}

func TestPodServiceAccountImagePullSecrets(t *testing.T) {
	var testCases = []struct {
		name           string
		catalogSource  *v1alpha1.CatalogSource
		serviceAccount *corev1.ServiceAccount
	}{
		{
			name: "ServiceAccount has no imagePullSecret",
			serviceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "",
					Name:      "service-account",
				},
			},
		},
		{
			name: "ServiceAccount has one imagePullSecret",
			serviceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "",
					Name:      "service-account",
				},
				ImagePullSecrets: []corev1.LocalObjectReference{{Name: "foo"}},
			},
		},
	}

	catalogSource := &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "testns",
		},
		Spec: v1alpha1.CatalogSourceSpec{
			GrpcPodConfig: &v1alpha1.GrpcPodConfig{
				ExtractContent: &v1alpha1.ExtractContentConfig{
					CacheDir:   "/tmp/cache",
					CatalogDir: "/catalog",
				},
			},
		},
	}

	for _, testCase := range testCases {
		pod, err := Pod(catalogSource, "name", "opmImage", "utilImage", "image", testCase.serviceAccount, map[string]string{}, map[string]string{}, int32(0), int32(0), int64(workloadUserID), v1alpha1.Legacy)
		require.NoError(t, err)
		if diff := cmp.Diff(testCase.serviceAccount.ImagePullSecrets, pod.Spec.ImagePullSecrets); diff != "" {
			t.Errorf("got incorrect pod: %v", diff)
		}
	}
}

func TestPodNodeSelector(t *testing.T) {
	catsrc := &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "testns",
		},
	}

	key := "kubernetes.io/os"
	value := "linux"

	gotCatSrcPod, err := Pod(catsrc, "hello", "utilImage", "opmImage", "busybox", serviceAccount("", "service-account"), map[string]string{}, map[string]string{}, int32(0), int32(0), int64(workloadUserID), v1alpha1.Legacy)
	require.NoError(t, err)
	gotCatSrcPodSelector := gotCatSrcPod.Spec.NodeSelector

	if gotCatSrcPodSelector[key] != value {
		t.Errorf("expected %s value for node selector key %s, received %s value instead", value, key,
			gotCatSrcPodSelector[key])
	}
}

func TestPullPolicy(t *testing.T) {
	var table = []struct {
		image  string
		policy corev1.PullPolicy
	}{
		{
			image:  "quay.io/operator-framework/olm@sha256:b9d011c0fbfb65b387904f8fafc47ee1a9479d28d395473341288ee126ed993b",
			policy: corev1.PullIfNotPresent,
		},
		{
			image:  "gcc@sha256:06a6f170d7fff592e44b089c0d2e68d870573eb9a23d9c66d4b6ea11f8fad18b",
			policy: corev1.PullIfNotPresent,
		},
		{
			image:  "myimage:1.0",
			policy: corev1.PullAlways,
		},
		{
			image:  "busybox",
			policy: corev1.PullAlways,
		},
		{
			image:  "gcc@sha256:06a6f170d7fff592e44b089c0d2e68",
			policy: corev1.PullIfNotPresent,
		},
		{
			image:  "hello@md5:b1946ac92492d2347c6235b4d2611184",
			policy: corev1.PullIfNotPresent,
		},
	}

	source := &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test-ns",
		},
	}

	for _, tt := range table {
		p, err := Pod(source, "catalog", "opmImage", "utilImage", tt.image, serviceAccount("", "service-account"), nil, nil, int32(0), int32(0), int64(workloadUserID), v1alpha1.Legacy)
		require.NoError(t, err)
		policy := p.Spec.Containers[0].ImagePullPolicy
		if policy != tt.policy {
			t.Fatalf("expected pull policy %s for image  %s", tt.policy, tt.image)
		}
	}
}

func TestPodContainerSecurityContext(t *testing.T) {
	testcases := []struct {
		title                            string
		inputCatsrc                      *v1alpha1.CatalogSource
		namespacePodSecurityConfig       v1alpha1.SecurityConfig
		expectedSecurityContext          *corev1.PodSecurityContext
		expectedContainerSecurityContext *corev1.SecurityContext
	}{
		{
			title: "NoSpecDefined/NamespaceRestricted/UseRestricted",
			inputCatsrc: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: testNamespace,
				},
			},
			namespacePodSecurityConfig: v1alpha1.Restricted,
			expectedContainerSecurityContext: &corev1.SecurityContext{
				AllowPrivilegeEscalation: ptr.To(false),
				Capabilities: &corev1.Capabilities{
					Drop: []corev1.Capability{"ALL"},
				},
				ReadOnlyRootFilesystem: ptr.To(false), // Reflecting expected 'restricted' settings
			},
			expectedSecurityContext: &corev1.PodSecurityContext{
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				RunAsNonRoot:   ptr.To(true),
				RunAsUser:      ptr.To(int64(workloadUserID)),
			},
		},
		{
			title:                      "NoSpecDefined/NamespaceNotRestricted/UseLegacy",
			namespacePodSecurityConfig: v1alpha1.Legacy,
			inputCatsrc: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: testNamespace,
				},
			},
			expectedContainerSecurityContext: &corev1.SecurityContext{ReadOnlyRootFilesystem: ptr.To(false)},
			expectedSecurityContext:          nil,
		},
		{
			title: "SpecDefined/NoGRPCPodConfig/NamespaceRestricted/UseRestricted",
			inputCatsrc: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: testNamespace,
				},
				Spec: v1alpha1.CatalogSourceSpec{},
			},
			namespacePodSecurityConfig: v1alpha1.Restricted,
			expectedContainerSecurityContext: &corev1.SecurityContext{
				AllowPrivilegeEscalation: ptr.To(false),
				Capabilities: &corev1.Capabilities{
					Drop: []corev1.Capability{"ALL"},
				},
				ReadOnlyRootFilesystem: ptr.To(false),
			},
			expectedSecurityContext: &corev1.PodSecurityContext{
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				RunAsNonRoot:   ptr.To(true),
				RunAsUser:      ptr.To(int64(workloadUserID)),
			},
		},
		{
			title: "SpecDefined/NoGRPCPodConfig/NamespaceNotRestricted/UseLegacy",
			inputCatsrc: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: testNamespace,
				},
				Spec: v1alpha1.CatalogSourceSpec{
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{},
				},
			},
			namespacePodSecurityConfig:       v1alpha1.Legacy,
			expectedContainerSecurityContext: &corev1.SecurityContext{ReadOnlyRootFilesystem: ptr.To(false)},
			expectedSecurityContext:          nil,
		},
		{
			title: "SpecDefined/SecurityContextConfig:Legacy/NoChangeExpected",
			inputCatsrc: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: testNamespace,
				},
				Spec: v1alpha1.CatalogSourceSpec{
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						SecurityContextConfig: v1alpha1.Legacy,
					},
				},
			},
			namespacePodSecurityConfig:       v1alpha1.Restricted, // set to the opposite of the config to catch possible errors
			expectedContainerSecurityContext: &corev1.SecurityContext{ReadOnlyRootFilesystem: ptr.To(false)},
			expectedSecurityContext:          nil,
		},
		{
			title: "SpecDefined/SecurityContextConfig:Restricted/RestrictedSecurityConfigApplied",
			inputCatsrc: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: testNamespace,
				},
				Spec: v1alpha1.CatalogSourceSpec{
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						SecurityContextConfig: v1alpha1.Restricted,
					},
				},
			},
			namespacePodSecurityConfig: v1alpha1.Legacy, // set to the opposite of the config to catch possible errors
			expectedContainerSecurityContext: &corev1.SecurityContext{
				ReadOnlyRootFilesystem:   ptr.To(false),
				AllowPrivilegeEscalation: ptr.To(false),
				Capabilities: &corev1.Capabilities{
					Drop: []corev1.Capability{"ALL"},
				},
			},
			expectedSecurityContext: &corev1.PodSecurityContext{
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				RunAsNonRoot:   ptr.To(true),
				RunAsUser:      ptr.To(int64(workloadUserID)),
			},
		},
	}

	for _, testcase := range testcases {
		t.Run(testcase.title, func(t *testing.T) {
			outputPod, err := Pod(testcase.inputCatsrc, "hello", "utilImage", "opmImage", "busybox", serviceAccount("", "service-account"), map[string]string{}, map[string]string{}, int32(0), int32(0), workloadUserID, testcase.namespacePodSecurityConfig)
			require.NoError(t, err)

			// Assert PodSecurityContext
			require.Equal(t, testcase.expectedSecurityContext, outputPod.Spec.SecurityContext)

			// Assert ContainerSecurityContext
			require.Equal(t, testcase.expectedContainerSecurityContext, outputPod.Spec.Containers[0].SecurityContext)
		})
	}
}

// TestPodAvoidsConcurrentWrite is a regression test for
// https://bugzilla.redhat.com/show_bug.cgi?id=2101357
// we were mutating the input annotations and labels parameters causing
// concurrent write issues
func TestPodAvoidsConcurrentWrite(t *testing.T) {
	catsrc := &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "testns",
		},
	}

	labels := map[string]string{
		"label": "something",
	}

	annotations := map[string]string{
		"annotation": "somethingelse",
	}

	gotPod, err := Pod(catsrc, "hello", "opmImage", "utilImage", "busybox", serviceAccount("", "service-account"), labels, annotations, int32(0), int32(0), int64(workloadUserID), v1alpha1.Legacy)
	require.NoError(t, err)

	// check labels and annotations point to different addresses between parameters and what's in the pod
	require.NotEqual(t, &labels, &gotPod.Labels)
	require.NotEqual(t, &annotations, &gotPod.Annotations)

	// check that labels and annotations from the parameters were copied down to the pod's
	require.Equal(t, labels["label"], gotPod.Labels["label"])
	require.Equal(t, annotations["annotation"], gotPod.Annotations["annotation"])
}

func TestPodSchedulingOverrides(t *testing.T) {
	// This test ensures that any overriding pod scheduling configuration elements
	// defined in spec.grpcPodConfig are applied to the catalog source pod created
	// when spec.sourceType = 'grpc' and spec.image is set.
	var tolerationSeconds int64 = 120
	var overriddenPriorityClassName = "some-prio-class"
	var overriddenNodeSelectors = map[string]string{
		"label":  "value",
		"label2": "value2",
	}
	var defaultNodeSelectors = map[string]string{
		"kubernetes.io/os": "linux",
	}
	var defaultPriorityClassName = ""

	var overriddenTolerations = []corev1.Toleration{
		{
			Key:               "some/key",
			Operator:          corev1.TolerationOpExists,
			Effect:            corev1.TaintEffectNoExecute,
			TolerationSeconds: &tolerationSeconds,
		},
		{
			Key:      "someother/key",
			Operator: corev1.TolerationOpEqual,
			Effect:   corev1.TaintEffectNoSchedule,
		},
	}

	var overriddenAffinity = &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{
					{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{
								Key:      "kubernetes.io/arch",
								Operator: corev1.NodeSelectorOpIn,
								Values: []string{
									"amd64",
									"arm",
								},
							},
						},
					},
				},
			},
		},
	}

	testCases := []struct {
		title                     string
		catalogSource             *v1alpha1.CatalogSource
		expectedNodeSelectors     map[string]string
		expectedTolerations       []corev1.Toleration
		expectedAffinity          *corev1.Affinity
		expectedPriorityClassName string
		annotations               map[string]string
	}{
		{
			title: "no overrides",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      "repo/image:tag",
				},
			},
			expectedTolerations:       nil,
			expectedAffinity:          nil,
			expectedPriorityClassName: defaultPriorityClassName,
			expectedNodeSelectors:     defaultNodeSelectors,
		}, {
			title: "override node selectors",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      "repo/image:tag",
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						NodeSelector: overriddenNodeSelectors,
					},
				},
			},
			expectedTolerations:       nil,
			expectedAffinity:          nil,
			expectedPriorityClassName: defaultPriorityClassName,
			expectedNodeSelectors:     overriddenNodeSelectors,
		}, {
			title: "override priority class name",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      "repo/image:tag",
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						PriorityClassName: &overriddenPriorityClassName,
					},
				},
			},
			expectedTolerations:       nil,
			expectedAffinity:          nil,
			expectedPriorityClassName: overriddenPriorityClassName,
			expectedNodeSelectors:     defaultNodeSelectors,
		}, {
			title: "doesn't override priority class name when its nil",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      "repo/image:tag",
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						PriorityClassName: nil,
					},
				},
			},
			expectedTolerations:       nil,
			expectedAffinity:          nil,
			expectedPriorityClassName: defaultPriorityClassName,
			expectedNodeSelectors:     defaultNodeSelectors,
		}, {
			title: "Override node tolerations",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      "repo/image:tag",
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						Tolerations: overriddenTolerations,
					},
				},
			},
			expectedTolerations:       overriddenTolerations,
			expectedAffinity:          nil,
			expectedPriorityClassName: defaultPriorityClassName,
			expectedNodeSelectors:     defaultNodeSelectors,
		}, {
			title: "Override affinity",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      "repo/image:tag",
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						Affinity: overriddenAffinity,
					},
				},
			},
			expectedTolerations:       nil,
			expectedAffinity:          overriddenAffinity,
			expectedPriorityClassName: defaultPriorityClassName,
			expectedNodeSelectors:     defaultNodeSelectors,
		}, {
			title: "Override all the things",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      "repo/image:tag",
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						NodeSelector:      overriddenNodeSelectors,
						PriorityClassName: &overriddenPriorityClassName,
						Tolerations:       overriddenTolerations,
						Affinity:          overriddenAffinity,
					},
				},
			},
			expectedTolerations:       overriddenTolerations,
			expectedAffinity:          overriddenAffinity,
			expectedPriorityClassName: overriddenPriorityClassName,
			expectedNodeSelectors:     overriddenNodeSelectors,
		}, {
			title: "priorityClassName annotation takes precedence",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      "repo/image:tag",
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						PriorityClassName: &overriddenPriorityClassName,
					},
				},
			},
			expectedTolerations: nil,
			expectedAffinity:    nil,
			annotations: map[string]string{
				CatalogPriorityClassKey: "some-OTHER-prio-class",
			},
			expectedPriorityClassName: "some-OTHER-prio-class",
			expectedNodeSelectors:     defaultNodeSelectors,
		},
	}

	for _, testCase := range testCases {
		pod, err := Pod(testCase.catalogSource, "hello", "opmImage", "utilImage", "busybox", serviceAccount("", "service-account"), map[string]string{}, testCase.annotations, int32(0), int32(0), int64(workloadUserID), v1alpha1.Legacy)
		require.NoError(t, err)
		require.Equal(t, testCase.expectedNodeSelectors, pod.Spec.NodeSelector)
		require.Equal(t, testCase.expectedPriorityClassName, pod.Spec.PriorityClassName)
		require.Equal(t, testCase.expectedTolerations, pod.Spec.Tolerations)
		require.Equal(t, testCase.expectedAffinity, pod.Spec.Affinity)
	}
}

// baseClusterState returns a list of runtime objects that are required for the tests to run including the
// target namespace with the assumed default configuration
func baseClusterState() []runtime.Object {
	return []runtime.Object{
		defaultNamespace(),
	}
}

// defaultNamespace returns a kubernetes namespace with the assumes default settings,
// e.g. Pod Security Admission security policy label
func defaultNamespace() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: testNamespace,
			Labels: map[string]string{
				// catalogsource pod security configuration depends on the defaultNamespace psa configuration
				// adding restricted PSA label as this is the default
				"pod-security.kubernetes.io/enforce": "restricted",
			},
		},
	}
}

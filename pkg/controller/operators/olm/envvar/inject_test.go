package envvar_test

import (
	"testing"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/olm/envvar"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
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
)

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
			envvar.InjectEnvIntoDeployment(tt.podSpec, tt.envVar)

			podSpecWant := tt.expected
			podSpecGot := tt.podSpec

			assert.Equal(t, podSpecWant, podSpecGot)
		})
	}
}

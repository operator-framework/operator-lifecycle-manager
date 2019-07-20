package proxy_test

import (
	"strings"
	"testing"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/proxy"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

var (
	globalProxyConfig = []corev1.EnvVar{
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

func TestIsOverridden(t *testing.T) {
	tests := []struct {
		name     string
		envVar   []corev1.EnvVar
		expected bool
	}{
		{
			name:     "WithEmptyEnvVar",
			envVar:   []corev1.EnvVar{},
			expected: false,
		},
		{
			name:     "WithNilEnvVar",
			envVar:   nil,
			expected: false,
		},
		{
			name: "WithUnrelatedEnvVar",
			envVar: []corev1.EnvVar{
				corev1.EnvVar{
					Name:  "foo",
					Value: "foo_value",
				},
			},
			expected: false,
		},
		{
			name: "WithHTTP_PROXY",
			envVar: []corev1.EnvVar{
				corev1.EnvVar{
					Name:  envHTTPProxyName,
					Value: "http://",
				},
			},
			expected: true,
		},
		{
			name: "WithHTTPS_PROXY",
			envVar: []corev1.EnvVar{
				corev1.EnvVar{
					Name:  envHTTPSProxyName,
					Value: "https://",
				},
			},
			expected: true,
		},
		{
			name: "WithNO_PROXY",
			envVar: []corev1.EnvVar{
				corev1.EnvVar{
					Name:  envNoProxyName,
					Value: "https://",
				},
			},
			expected: true,
		},
		{
			name: "WithCaseSensitive",
			envVar: []corev1.EnvVar{
				corev1.EnvVar{
					Name:  strings.ToLower(envHTTPSProxyName),
					Value: "http://",
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := proxy.IsOverridden(tt.envVar)

			assert.Equal(t, tt.expected, actual)
		})
	}
}

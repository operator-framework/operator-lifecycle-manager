package proxy_test

import (
	"testing"

	apiconfigv1 "github.com/openshift/api/config/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/proxy"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

const (
	envHTTPProxyName  = "HTTP_PROXY"
	envHTTPSProxyName = "HTTPS_PROXY"
	envNoProxyName    = "NO_PROXY"
)

func TestToEnvVar(t *testing.T) {
	tests := []struct {
		name       string
		proxy      *apiconfigv1.Proxy
		envVarWant []corev1.EnvVar
	}{
		{
			name: "WithSet",
			proxy: &apiconfigv1.Proxy{
				Status: apiconfigv1.ProxyStatus{
					HTTPProxy:  "http://",
					HTTPSProxy: "https://",
					NoProxy:    "foo,bar",
				},
			},
			envVarWant: []corev1.EnvVar{
				corev1.EnvVar{
					Name:  envHTTPProxyName,
					Value: "http://",
				},
				corev1.EnvVar{
					Name:  envHTTPSProxyName,
					Value: "https://",
				},
				corev1.EnvVar{
					Name:  envNoProxyName,
					Value: "foo,bar",
				},
			},
		},

		{
			name: "WithUnset",
			proxy: &apiconfigv1.Proxy{
				Status: apiconfigv1.ProxyStatus{
					HTTPProxy:  "http://",
					HTTPSProxy: "",
					NoProxy:    "",
				},
			},
			envVarWant: []corev1.EnvVar{
				corev1.EnvVar{
					Name:  envHTTPProxyName,
					Value: "http://",
				},
				corev1.EnvVar{
					Name:  envHTTPSProxyName,
					Value: "",
				},
				corev1.EnvVar{
					Name:  envNoProxyName,
					Value: "",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envVarGot := proxy.ToEnvVar(tt.proxy)

			assert.NotNil(t, envVarGot)
			assert.Equal(t, tt.envVarWant, envVarGot)

		})
	}

}

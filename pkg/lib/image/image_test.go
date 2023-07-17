package image_test

import (
	"testing"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/image"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestInferImagePullPolicy(t *testing.T) {
	tests := []struct {
		description    string
		img            string
		expectedPolicy corev1.PullPolicy
	}{
		{
			description:    "WithImageTag",
			img:            "my-image:my-tag",
			expectedPolicy: corev1.PullAlways,
		},
		{
			description:    "WithImageDigest",
			img:            "my-image@sha256:54d626e08c1c802b305dad30b7e54a82f102390cc92c7d4db112048935236e9c",
			expectedPolicy: corev1.PullIfNotPresent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			policy := image.InferImagePullPolicy(tt.img)
			require.Equal(t, tt.expectedPolicy, policy)
		})
	}
}

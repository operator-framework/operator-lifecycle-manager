package image

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

func InferImagePullPolicy(image string) corev1.PullPolicy {
	// Ensure the image is always pulled if the image is not based on a digest, measured by whether an "@" is included.
	// See https://github.com/docker/distribution/blob/master/reference/reference.go for more info.
	// This means recreating non-digest based pods will result in the latest version of the content being delivered on-cluster.
	if strings.Contains(image, "@") {
		return corev1.PullIfNotPresent
	}

	// Ensure test registry images are pulled only if needed
	// These will normally be loaded through kind load docker-image
	// This is also helps support the e2e fixture image re-build flow (see e2e GHA)
	if strings.HasPrefix(image, "quay.io/olmtest/") {
		return corev1.PullIfNotPresent
	}
	return corev1.PullAlways
}

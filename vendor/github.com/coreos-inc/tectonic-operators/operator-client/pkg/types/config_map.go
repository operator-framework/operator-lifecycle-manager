package types

import "k8s.io/api/core/v1"

// ConfigMapModifier is a modifier function to be used when atomically
// updating a ConfigMap.
type ConfigMapModifier func(*v1.ConfigMap) error

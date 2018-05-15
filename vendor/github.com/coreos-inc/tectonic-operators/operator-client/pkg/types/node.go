package types

import "k8s.io/api/core/v1"

// NodeModifier is a function that can modify a
// Node during an atomic update.
type NodeModifier func(*v1.Node) error

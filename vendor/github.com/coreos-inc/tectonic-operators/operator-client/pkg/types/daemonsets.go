package types

import (
	appsv1beta2 "k8s.io/api/apps/v1beta2"
)

// DaemonSetUpdateFn is a function that runs as a modifier function to update
// the DaemonSet before sending it back to the API Server. This function will
// run after any before / during updates that have been specified.
type DaemonSetUpdateFn func(*appsv1beta2.DaemonSet) error

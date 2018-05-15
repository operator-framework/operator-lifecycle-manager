package types

import (
	appsv1beta2 "k8s.io/api/apps/v1beta2"
)

// DeploymentUpdateFn is a function that runs as a modifier function to update
// the Deployment before sending it back to the API Server. This function will
// run after any before / during updates that have been specified.
type DeploymentUpdateFn func(dep *appsv1beta2.Deployment) error

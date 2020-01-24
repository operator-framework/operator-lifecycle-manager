// +build tools

package tools

import (
	_ "github.com/golang/mock/mockgen"
	_ "github.com/maxbrunsfeld/counterfeiter/v6"
	_ "github.com/mikefarah/yq/v2"
	_ "k8s.io/code-generator"
	_ "k8s.io/kube-openapi/cmd/openapi-gen"
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen"
	_ "helm.sh/helm/v3/cmd/helm"
)

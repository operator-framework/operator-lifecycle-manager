// +build tools

package tools

import (
	_ "github.com/golang/mock/mockgen"
	_ "github.com/maxbrunsfeld/counterfeiter/v6"
	_ "k8s.io/code-generator"
	_ "k8s.io/kube-openapi/cmd/openapi-gen"
)

//go:build !kind

package ctx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"k8s.io/client-go/tools/clientcmd"
)

func Provision(ctx *TestContext) (func(), error) {
	path := os.Getenv("KUBECONFIG")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to determine kubeconfig path: %s", err.Error())
		}
		path = filepath.Join(home, ".kube", "config")
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open kubeconfig: %s", err.Error())
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("failed to read kubeconfig: %s", err.Error())
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(data)
	if err != nil {
		return nil, fmt.Errorf("error loading kubeconfig: %s", err.Error())
	}

	fmt.Printf("e2e cluster kubeconfig: %s\n", path)
	ctx.restConfig = restConfig
	ctx.kubeconfigPath = path

	return func() {}, setDerivedFields(ctx)
}

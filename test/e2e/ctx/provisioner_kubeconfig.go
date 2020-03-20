// +build !kind

package ctx

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	"k8s.io/client-go/rest"
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
	if os.IsNotExist(err) {
		// try in-cluster config
		// see https://github.com/coreos/etcd-operator/issues/731#issuecomment-283804819
		if len(os.Getenv("KUBERNETES_SERVICE_HOST")) == 0 {
			addrs, err := net.LookupHost("kubernetes.default.svc")
			if err != nil {
				return nil, fmt.Errorf("failed to resolve kubernetes service: %s", err.Error())
			}
			os.Setenv("KUBERNETES_SERVICE_HOST", addrs[0])
		}

		if len(os.Getenv("KUBERNETES_SERVICE_PORT")) == 0 {
			os.Setenv("KUBERNETES_SERVICE_PORT", "443")
		}

		restConfig, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve in-cluster config: %s", err.Error())
		}

		ctx.restConfig = restConfig
		return func() {}, setDerivedFields(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open kubeconfig: %s", err.Error())
	}
	defer f.Close()

	const MaxKubeconfigBytes = 65535
	var b bytes.Buffer
	n, err := b.ReadFrom(io.LimitReader(f, MaxKubeconfigBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to read kubeconfig: %s", err.Error())
	}
	if n >= MaxKubeconfigBytes {
		return nil, fmt.Errorf("kubeconfig larger than maximum allowed size: %d bytes", MaxKubeconfigBytes)
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("error loading kubeconfig: %s", err.Error())
	}

	ctx.restConfig = restConfig

	return func() {}, setDerivedFields(ctx)
}

// +build kind

package ctx

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/kind/pkg/cluster"
	"sigs.k8s.io/kind/pkg/cluster/nodeutils"
	"sigs.k8s.io/kind/pkg/log"
)

var (
	images = flag.String("kind.images", "", "comma-separated list of image archives to load on cluster nodes, relative to the test binary or test package path")
)

type kindLogAdapter struct {
	*TestContext
}

var _ log.Logger = kindLogAdapter{}

func (kl kindLogAdapter) Enabled() bool {
	return true
}

func (kl kindLogAdapter) Info(message string) {
	kl.Infof(message)
}

func (kl kindLogAdapter) Infof(format string, args ...interface{}) {
	kl.Logf(format, args)
}

func (kl kindLogAdapter) Warn(message string) {
	kl.Warnf(message)
}

func (kl kindLogAdapter) Warnf(format string, args ...interface{}) {
	kl.Logf(format, args)
}

func (kl kindLogAdapter) Error(message string) {
	kl.Errorf(message)
}

func (kl kindLogAdapter) Errorf(format string, args ...interface{}) {
	kl.Logf(format, args)
}

func (kl kindLogAdapter) V(log.Level) log.InfoLogger {
	return kl
}

func Provision(ctx *TestContext) (func(), error) {
	dir, err := ioutil.TempDir("", "kind.")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary directory: %s", err.Error())
	}
	defer os.RemoveAll(dir)
	kubeconfigPath := filepath.Join(dir, "kubeconfig")

	provider := cluster.NewProvider(
		cluster.ProviderWithLogger(kindLogAdapter{ctx}),
	)
	name := fmt.Sprintf("kind-%s", rand.String(16))
	if err := provider.Create(
		name,
		cluster.CreateWithWaitForReady(5*time.Minute),
		cluster.CreateWithKubeconfigPath(kubeconfigPath),
	); err != nil {
		return nil, fmt.Errorf("failed to create kind cluster: %s", err.Error())
	}

	nodes, err := provider.ListNodes(name)
	if err != nil {
		return nil, fmt.Errorf("failed to list kind nodes: %s", err.Error())
	}

	var archives []string
	if images != nil {
		records, err := csv.NewReader(strings.NewReader(*images)).ReadAll()
		if err != nil {
			return nil, fmt.Errorf("error parsing image flag: %s", err.Error())
		}
		for _, row := range records {
			archives = append(archives, row...)
		}
	}

	for _, archive := range archives {
		for _, node := range nodes {
			fd, err := os.Open(archive)
			if err != nil {
				return nil, fmt.Errorf("error opening archive %q: %s", archive, err.Error())
			}
			err = nodeutils.LoadImageArchive(node, fd)
			fd.Close()
			if err != nil {
				return nil, fmt.Errorf("error loading image archive %q to node %q: %s", archive, node, err.Error())
			}
		}
	}

	kubeconfig, err := provider.KubeConfig(name, false)
	if err != nil {
		return nil, fmt.Errorf("failed to read kubeconfig: %s", err.Error())
	}
	restConfig, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfig))
	if err != nil {
		return nil, fmt.Errorf("error loading kubeconfig: %s", err.Error())
	}

	ctx.restConfig = restConfig

	var once sync.Once
	deprovision := func() {
		once.Do(func() {
			provider.Delete(name, kubeconfigPath)
		})
	}

	return deprovision, setDerivedFields(ctx)
}

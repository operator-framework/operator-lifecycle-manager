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
	logDir = "logs"

	verbosity int
)

func init() {
	// https://github.com/kubernetes-sigs/kind/blob/v0.10.0/pkg/log/types.go#L38-L45
	flag.IntVar(&verbosity, "kind.verbosity", 0, "log verbosity level")
}

type kindLogAdapter struct {
	*TestContext
}

var (
	_ log.Logger     = kindLogAdapter{}
	_ log.InfoLogger = kindLogAdapter{}
)

func (kl kindLogAdapter) Enabled() bool {
	return true
}

func (kl kindLogAdapter) Info(message string) {
	kl.Infof("%s", message)
}

func (kl kindLogAdapter) Infof(format string, args ...interface{}) {
	kl.Logf(format, args...)
}

func (kl kindLogAdapter) Warn(message string) {
	kl.Warnf("%s", message)
}

func (kl kindLogAdapter) Warnf(format string, args ...interface{}) {
	kl.Logf(format, args...)
}

func (kl kindLogAdapter) Error(message string) {
	kl.Errorf("%s", message)
}

func (kl kindLogAdapter) Errorf(format string, args ...interface{}) {
	kl.Logf(format, args...)
}

func (kl kindLogAdapter) V(level log.Level) log.InfoLogger {
	if level > log.Level(verbosity) {
		return log.NoopInfoLogger{}
	}
	return kl
}

func Provision(ctx *TestContext) (func(), error) {
	dir, err := ioutil.TempDir("", "kind.")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary directory: %s", err.Error())
	}
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

	if artifactsDir := os.Getenv("ARTIFACTS_DIR"); artifactsDir != "" {
		ctx.artifactsDir = artifactsDir
	}
	ctx.kubeconfigPath = kubeconfigPath

	var once sync.Once
	deprovision := func() {
		once.Do(func() {
			// remove the temporary kubeconfig directory
			if err := os.RemoveAll(dir); err != nil {
				ctx.Logf("failed to remove the %s kubeconfig directory: %v", dir, err)
			}
			if ctx.artifactsDir != "" {
				ctx.Logf("collecting container logs for the %s cluster", name)
				if err := provider.CollectLogs(name, filepath.Join(ctx.artifactsDir, logDir)); err != nil {
					ctx.Logf("failed to collect logs: %v", err)
				}
			}
			provider.Delete(name, kubeconfigPath)
		})
	}

	return deprovision, setDerivedFields(ctx)
}

// +build helm

package ctx

import (
	"fmt"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli/values"
	"helm.sh/helm/v3/pkg/getter"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/assets/chart"
)

// clientAdapter implements genericclioptions.RESTClientGetter and
// clientcmd.ClientConfig around *rest.Config, in order to satisfy
// Helm.
type clientAdapter struct {
	*rest.Config
}

func (a clientAdapter) ToRESTConfig() (*rest.Config, error) {
	if a.Config == nil {
		return nil, fmt.Errorf("REST config is nil")
	}
	return a.Config, nil
}

func (a clientAdapter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	cfg, err := a.ToRESTConfig()
	if err != nil {
		return nil, err
	}
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return memory.NewMemCacheClient(dc), nil
}

func (a clientAdapter) ToRESTMapper() (meta.RESTMapper, error) {
	dc, err := a.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(dc)
	return restmapper.NewShortcutExpander(mapper, dc), nil
}

func (a clientAdapter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return clientcmd.ClientConfig(a)
}

func (a clientAdapter) RawConfig() (clientcmdapi.Config, error) {
	return clientcmdapi.Config{}, fmt.Errorf("not supported")
}

func (a clientAdapter) ClientConfig() (*rest.Config, error) {
	return a.ToRESTConfig()
}

func (a clientAdapter) Namespace() (string, bool, error) {
	return "default", false, nil
}

func (a clientAdapter) ConfigAccess() clientcmd.ConfigAccess {
	return clientcmd.NewDefaultClientConfigLoadingRules()
}

func Install(ctx *TestContext) error {
	cfg := action.Configuration{}
	cfg.Init(clientAdapter{ctx.RESTConfig()}, "", "memory", ctx.Logf)
	act := action.NewInstall(&cfg)
	act.Timeout = 5 * time.Second
	act.ReleaseName = fmt.Sprintf("release-%s", rand.String(8))

	var files []*loader.BufferedFile
	for _, name := range chart.AssetNames() {
		data, err := chart.Asset(name)
		if err != nil {
			return err
		}
		files = append(files, &loader.BufferedFile{Name: name, Data: data})
	}

	valueOptions := values.Options{
		Values: []string{
			"debug=true",
			"olm.image.ref=quay.io/operator-framework/olm:local",
			"olm.image.pullPolicy=IfNotPresent",
			"catalog.image.ref=quay.io/operator-framework/olm:local",
			"catalog.image.pullPolicy=IfNotPresent",
			"package.image.ref=quay.io/operator-framework/olm:local",
			"package.image.pullPolicy=IfNotPresent",
		},
	}

	chart, err := loader.LoadFiles(files)
	if err != nil {
		return err
	}

	values, err := valueOptions.MergeValues(getter.Providers(nil))
	if err != nil {
		return err
	}

	if _, err := act.Run(chart, values); err != nil {
		return err
	}
	return nil
}

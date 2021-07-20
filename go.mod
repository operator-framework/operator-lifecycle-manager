module github.com/operator-framework/operator-lifecycle-manager

go 1.16

require (
	github.com/blang/semver/v4 v4.0.0
	github.com/bshuster-repo/logrus-logstash-hook v1.0.0 // indirect
	github.com/coreos/go-semver v0.3.0
	github.com/davecgh/go-spew v1.1.1
	github.com/fsnotify/fsnotify v1.4.9
	github.com/ghodss/yaml v1.0.0
	github.com/go-bindata/go-bindata/v3 v3.1.3
	github.com/go-logr/logr v0.4.0
	github.com/golang/mock v1.4.1
	github.com/google/go-cmp v0.5.6
	github.com/googleapis/gnostic v0.5.5
	github.com/irifrance/gini v1.0.1
	github.com/itchyny/gojq v0.11.0
	github.com/maxbrunsfeld/counterfeiter/v6 v6.2.2
	github.com/mikefarah/yq/v3 v3.0.0-20201202084205-8846255d1c37
	github.com/mitchellh/hashstructure v1.0.0
	github.com/mitchellh/mapstructure v1.1.2
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.13.0
	github.com/openshift/api v0.0.0-20200331152225-585af27e34fd
	github.com/openshift/client-go v0.0.0-20200326155132-2a6cd50aedd0
	github.com/operator-framework/api v0.10.1
	github.com/operator-framework/operator-registry v1.17.5
	github.com/otiai10/copy v1.2.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.11.0
	github.com/prometheus/client_model v0.2.0
	github.com/prometheus/common v0.26.0
	github.com/sirupsen/logrus v1.8.1
	github.com/spf13/cobra v1.1.3
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.7.0
	golang.org/x/time v0.0.0-20210611083556-38a9dc6acbc6
	google.golang.org/grpc v1.38.0
	gopkg.in/yaml.v2 v2.4.0
	helm.sh/helm/v3 v3.6.1
	k8s.io/api v0.22.0-beta.0
	k8s.io/apiextensions-apiserver v0.22.0-beta.0
	k8s.io/apimachinery v0.22.0-beta.0
	k8s.io/apiserver v0.22.0-beta.0
	k8s.io/client-go v0.22.0-beta.0
	k8s.io/code-generator v0.22.0-beta.0
	k8s.io/component-base v0.22.0-beta.0
	k8s.io/klog v1.0.0
	k8s.io/kube-aggregator v0.20.4
	k8s.io/kube-openapi v0.0.0-20210527164424-3c818078ee3d
	k8s.io/utils v0.0.0-20210527160623-6fdb442a123b
	rsc.io/letsencrypt v0.0.3 // indirect
	sigs.k8s.io/controller-runtime v0.9.2
	sigs.k8s.io/controller-tools v0.6.1
	sigs.k8s.io/kind v0.11.1
)

replace (
	// controller runtime
	github.com/openshift/api => github.com/openshift/api v0.0.0-20200331152225-585af27e34fd // release-4.5
	github.com/openshift/client-go => github.com/openshift/client-go v0.0.0-20200326155132-2a6cd50aedd0 // release-4.5
	// Patch for a race condition involving metadata-only
	// informers until it can be resolved upstream:
	sigs.k8s.io/controller-runtime v0.9.2 => github.com/benluddy/controller-runtime v0.9.3-0.20210720171926-9bcb99bd9bd3

	// pinned because no tag supports 1.18 yet
	sigs.k8s.io/structured-merge-diff => sigs.k8s.io/structured-merge-diff v1.0.1-0.20191108220359-b1b620dd3f06
)

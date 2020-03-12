module github.com/operator-framework/operator-lifecycle-manager

go 1.12

require (
	github.com/blang/semver v3.5.1+incompatible
	github.com/coreos/go-semver v0.3.0
	github.com/fsnotify/fsnotify v1.4.7
	github.com/ghodss/yaml v1.0.0
	github.com/go-openapi/spec v0.19.4
	github.com/golang/mock v1.3.1
	github.com/maxbrunsfeld/counterfeiter/v6 v6.2.2
	github.com/mikefarah/yq/v2 v2.4.1
	github.com/mitchellh/hashstructure v1.0.0
	github.com/openshift/api v3.9.1-0.20190924102528-32369d4db2ad+incompatible
	github.com/openshift/client-go v0.0.0-20190923180330-3b6373338c9b
	github.com/operator-framework/operator-registry v1.5.8
	github.com/otiai10/copy v1.0.1
	github.com/pkg/errors v0.8.1
	github.com/prometheus/client_golang v1.2.1
	github.com/sirupsen/logrus v1.4.2
	github.com/spf13/cobra v0.0.5
	github.com/stretchr/testify v1.4.0
	golang.org/x/sys v0.0.0-20191210023423-ac6580df4449 // indirect
	golang.org/x/time v0.0.0-20191024005414-555d28b269f0
	google.golang.org/grpc v1.24.0
	gopkg.in/yaml.v2 v2.2.4
	helm.sh/helm/v3 v3.0.1
	k8s.io/api v0.17.1
	k8s.io/apiextensions-apiserver v0.17.1
	k8s.io/apimachinery v0.17.1
	k8s.io/apiserver v0.17.1
	k8s.io/client-go v8.0.0+incompatible
	k8s.io/code-generator v0.17.1
	k8s.io/component-base v0.17.1
	k8s.io/klog v1.0.0
	k8s.io/kube-aggregator v0.0.0
	k8s.io/kube-openapi v0.0.0-20191107075043-30be4d16710a
	k8s.io/kubernetes v1.17.1
	sigs.k8s.io/controller-tools v0.2.4
)

replace (
	github.com/docker/docker => github.com/moby/moby v0.7.3-0.20190826074503-38ab9da00309 // Required by Helm
	github.com/openshift/api => github.com/openshift/api v3.9.1-0.20190924102528-32369d4db2ad+incompatible
	github.com/openshift/client-go => github.com/openshift/client-go v0.0.0-20190923180330-3b6373338c9b
	github.com/prometheus/client_golang => github.com/prometheus/client_golang v0.9.3-0.20190127221311-3c4408c8b829
	helm.sh/helm/v3 => helm.sh/helm/v3 v3.0.0-beta.5.0.20200123114618-5e3c7d7eb86a

	// Pin to kube 1.17
	k8s.io/api => k8s.io/api v0.17.1
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.17.1
	k8s.io/apimachinery => github.com/openshift/kubernetes-apimachinery v0.0.0-20191211181342-5a804e65bdc1
	k8s.io/apiserver => k8s.io/apiserver v0.17.1
	k8s.io/cli-runtime => github.com/openshift/kubernetes-cli-runtime v0.0.0-20200114162348-c8810ef308ee
	k8s.io/client-go => github.com/openshift/kubernetes-client-go v0.0.0-20191211181558-5dcabadb2b45
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.17.1
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.17.1
	k8s.io/code-generator => k8s.io/code-generator v0.17.1
	k8s.io/component-base => k8s.io/component-base v0.17.1
	k8s.io/cri-api => k8s.io/cri-api v0.17.1
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.17.1
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.17.1
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.17.1
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.17.1
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.17.1
	k8s.io/kubectl => github.com/openshift/kubernetes-kubectl v0.0.0-20200211153013-50adac736181
	k8s.io/kubelet => k8s.io/kubelet v0.17.1
	k8s.io/kubernetes => github.com/openshift/kubernetes v1.17.0-alpha.0.0.20191216151305-079984b0a154
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.17.1
	k8s.io/metrics => k8s.io/metrics v0.17.1
	k8s.io/node-api => k8s.io/node-api v0.17.1
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.17.1
	k8s.io/sample-cli-plugin => k8s.io/sample-cli-plugin v0.17.1
	k8s.io/sample-controller => k8s.io/sample-controller v0.17.1
)

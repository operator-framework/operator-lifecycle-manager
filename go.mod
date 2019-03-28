module github.com/operator-framework/operator-lifecycle-manager

require (
	github.com/Azure/go-ansiterm v0.0.0-20170929234023-d6e3b3328b78 // indirect
	github.com/Sirupsen/logrus v0.0.0-00010101000000-000000000000 // indirect
	github.com/coreos/bbolt v1.3.2 // indirect
	github.com/coreos/etcd v3.3.12+incompatible // indirect
	github.com/coreos/go-semver v0.2.0
	github.com/coreos/go-systemd v0.0.0-20190204112023-081494f7ee4f // indirect
	github.com/docker/distribution v2.7.1+incompatible // indirect
	github.com/docker/docker v1.13.1 // indirect
	github.com/emicklei/go-restful v2.9.0+incompatible // indirect
	github.com/ghodss/yaml v1.0.0
	github.com/go-openapi/spec v0.17.2
	github.com/go-openapi/strfmt v0.19.0 // indirect
	github.com/go-openapi/validate v0.19.0 // indirect
	github.com/gogo/protobuf v1.2.0 // indirect
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/golang/groupcache v0.0.0-20190129154638-5b532d6fd5ef // indirect
	github.com/golang/mock v1.1.1
	github.com/google/btree v1.0.0 // indirect
	github.com/gregjones/httpcache v0.0.0-20181110185634-c63ab54fda8f // indirect
	github.com/grpc-ecosystem/grpc-gateway v1.7.0 // indirect
	github.com/json-iterator/go v1.1.6 // indirect
	github.com/maxbrunsfeld/counterfeiter v0.0.0-20181017030959-1aadac120687
	github.com/openshift/api v3.9.1-0.20190321190659-71fdeba18656+incompatible
	github.com/openshift/client-go v0.0.0-20190313214351-8ae2a9c33ba2
	github.com/operator-framework/operator-registry v1.0.6
	github.com/pkg/errors v0.8.0
	github.com/prometheus/client_golang v0.9.1
	github.com/prometheus/client_model v0.0.0-20190115171406-56726106282f // indirect
	github.com/prometheus/common v0.2.0 // indirect
	github.com/prometheus/procfs v0.0.0-20190117184657-bf6a532e95b1 // indirect
	github.com/sirupsen/logrus v1.2.0
	github.com/spf13/cobra v0.0.3
	github.com/stretchr/testify v1.2.2
	go.etcd.io/bbolt v1.3.2 // indirect
	golang.org/x/time v0.0.0-20181108054448-85acf8d2951c
	google.golang.org/grpc v1.16.0
	k8s.io/api v0.0.0-20190118113203-912cbe2bfef3
	k8s.io/apiextensions-apiserver v0.0.0-20190223021643-57c81b676ab1
	k8s.io/apimachinery v0.0.0-20190223001710-c182ff3b9841
	k8s.io/apiserver v0.0.0-20181026151315-13cfe3978170
	k8s.io/client-go v8.0.0+incompatible
	k8s.io/code-generator v0.0.0-20181203235156-f8cba74510f3
	k8s.io/gengo v0.0.0-20190128074634-0689ccc1d7d6 // indirect
	k8s.io/klog v0.2.0 // indirect
	k8s.io/kube-aggregator v0.0.0-20190223015803-f706565beac0
	k8s.io/kube-openapi v0.0.0-20181031203759-72693cb1fadd
	k8s.io/kubernetes v1.12.7
)

replace (
	// This is necessary due to the combination of logrus casing changing
	// without bumping the major version and the extremely old version of
	// docker that's being pulled in.
	// original breakage - https://github.com/sirupsen/logrus/issues/451
	// breakage explanation - https://github.com/golang/go/issues/26208#issuecomment-411955266
	github.com/Sirupsen/logrus => github.com/sirupsen/logrus v1.1.0
	// all of the below are using the kubernetes-1.12.7 tag
	// remember to bump kubernetes above also when upgrading
	k8s.io/api => k8s.io/api v0.0.0-20190325144926-266ff08fa05d
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.0.0-20190325151511-42d4d5ce2c84
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20190221084156-01f179d85dbc
	k8s.io/apiserver => k8s.io/apiserver v0.0.0-20190325150012-164c02b49fbe
	k8s.io/client-go => k8s.io/client-go v2.0.0-alpha.0.0.20190325145348-5392b64e5c0b+incompatible
	k8s.io/code-generator => k8s.io/code-generator v0.0.0-20181128191024-b1289fc74931
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.0.0-20190325150400-0a029fc09217
)

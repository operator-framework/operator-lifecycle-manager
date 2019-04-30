module github.com/operator-framework/operator-lifecycle-manager

go 1.12

require (
	github.com/Azure/go-ansiterm v0.0.0-20170929234023-d6e3b3328b78 // indirect
	github.com/blang/semver v3.5.1+incompatible
	github.com/coreos/etcd v3.3.12+incompatible // indirect
	github.com/coreos/go-semver v0.2.0
	github.com/coreos/go-systemd v0.0.0-20190321100706-95778dfbb74e // indirect
	github.com/docker/distribution v2.7.1+incompatible // indirect
	github.com/docker/docker v0.7.3-0.20190409004836-2e1cfbca03da // indirect
	github.com/emicklei/go-restful v2.9.3+incompatible // indirect
	github.com/ghodss/yaml v1.0.0
	github.com/globalsign/mgo v0.0.0-20181015135952-eeefdecb41b8 // indirect
	github.com/go-openapi/analysis v0.17.2 // indirect
	github.com/go-openapi/errors v0.17.2 // indirect
	github.com/go-openapi/jsonpointer v0.19.0 // indirect
	github.com/go-openapi/jsonreference v0.19.0 // indirect
	github.com/go-openapi/loads v0.17.2 // indirect
	github.com/go-openapi/runtime v0.17.2 // indirect
	github.com/go-openapi/spec v0.19.0
	github.com/go-openapi/swag v0.17.2 // indirect
	github.com/gogo/protobuf v1.2.0 // indirect
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/golang/mock v1.2.1-0.20190329180013-73dc87cad333
	github.com/google/btree v1.0.0 // indirect
	github.com/google/go-cmp v0.2.0 // indirect
	github.com/google/gofuzz v1.0.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway v1.8.5 // indirect
	github.com/json-iterator/go v1.1.6 // indirect
	github.com/maxbrunsfeld/counterfeiter/v6 v6.0.2
	github.com/onsi/ginkgo v1.8.0 // indirect
	github.com/openshift/api v3.9.1-0.20190424152011-77b8897ec79a+incompatible
	github.com/openshift/client-go v0.0.0-20190401163519-84c2b942258a
	github.com/operator-framework/operator-registry v1.1.0
	github.com/pkg/errors v0.8.1
	github.com/prometheus/client_golang v0.9.2
	github.com/sirupsen/logrus v1.4.1
	github.com/spf13/cobra v0.0.3
	github.com/stretchr/testify v1.2.2
	go.uber.org/zap v1.10.0 // indirect
	golang.org/x/crypto v0.0.0-20190404164418-38d8ce5564a5 // indirect
	golang.org/x/text v0.3.1-0.20181227161524-e6919f6577db // indirect
	golang.org/x/time v0.0.0-20190308202827-9d24e82272b4
	google.golang.org/appengine v1.5.0 // indirect
	google.golang.org/grpc v1.19.1
	gotest.tools v2.2.0+incompatible // indirect
	k8s.io/api v0.0.0-20190118113203-912cbe2bfef3
	k8s.io/apiextensions-apiserver v0.0.0-20190221101132-cda7b6cfba78
	k8s.io/apimachinery v0.0.0-20190221084156-01f179d85dbc
	k8s.io/apiserver v0.0.0-20190402012035-5e1c1f41ee34
	k8s.io/cli-runtime v0.0.0-20190221101700-11047e25a94a // indirect
	k8s.io/client-go v11.0.0+incompatible
	k8s.io/code-generator v0.0.0-20181203235156-f8cba74510f3
	k8s.io/gengo v0.0.0-20190327210449-e17681d19d3a // indirect
	k8s.io/klog v0.2.0 // indirect
	k8s.io/kube-aggregator v0.0.0-20190221095344-e77f03c95d65
	k8s.io/kube-openapi v0.0.0-20190401085232-94e1e7b7574c
	k8s.io/kubernetes v1.12.8
	k8s.io/utils v0.0.0-20190308190857-21c4ce38f2a7 // indirect
)

replace (
	// pin kube dependencies to release-1.12 branch
	github.com/evanphx/json-patch => github.com/evanphx/json-patch v0.0.0-20190203023257-5858425f7550
	k8s.io/api => k8s.io/api v0.0.0-20181128191700-6db15a15d2d3
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.0.0-20190221101132-cda7b6cfba78
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20190221084156-01f179d85dbc
	k8s.io/apiserver => k8s.io/apiserver v0.0.0-20190402012035-5e1c1f41ee34
	k8s.io/client-go => k8s.io/client-go v0.0.0-20190228133956-77e032213d34
	k8s.io/code-generator => k8s.io/code-generator v0.0.0-20181128191024-b1289fc74931
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.0.0-20190221095344-e77f03c95d65
	k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20180711000925-0cf8f7e6ed1d
)

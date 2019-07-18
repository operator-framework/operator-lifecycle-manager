module github.com/operator-framework/operator-lifecycle-manager

go 1.12

replace (
	// Pin openshift version to 4.2 (uses kube 1.14)
	github.com/openshift/api => github.com/openshift/api v3.9.1-0.20190717200738-0390d1e77d64+incompatible
	github.com/openshift/client-go => github.com/openshift/client-go v0.0.0-20190627172412-c44a8b61b9f4

	// Pin kube version to 1.14
	k8s.io/api => k8s.io/api v0.0.0-20190704095032-f4ca3d3bdf1d
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.0.0-20190704104557-6209bbe9f7a9
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20190704094733-8f6ac2502e51
	k8s.io/apiserver => k8s.io/apiserver v0.0.0-20190704101451-e5f5c6e528cd
	k8s.io/client-go => k8s.io/client-go v11.0.1-0.20190521191137-11646d1007e0+incompatible
	k8s.io/code-generator => k8s.io/code-generator v0.0.0-20190704094409-6c2a4329ac29
	k8s.io/component-base => k8s.io/component-base v0.0.0-20190704100636-f0322db00a10
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.0.0-20190704101955-e796fd6d55e0
	k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20190228160746-b3a7cee44a30
	k8s.io/kubernetes => k8s.io/kubernetes v1.14.5-beta.0.0.20190708100021-7936da50c68f
	sigs.k8s.io/structured-merge-diff => sigs.k8s.io/structured-merge-diff v0.0.0-20190302045857-e85c7b244fd2
)

require (
	github.com/blang/semver v3.5.1+incompatible
	github.com/coreos/go-semver v0.3.0
	github.com/coreos/go-systemd v0.0.0-20190620071333-e64a0ec8b42a // indirect
	github.com/docker/distribution v2.7.1+incompatible // indirect
	github.com/emicklei/go-restful v2.9.6+incompatible // indirect
	github.com/evanphx/json-patch v4.5.0+incompatible // indirect
	github.com/ghodss/yaml v1.0.0
	github.com/go-openapi/spec v0.19.2
	github.com/go-openapi/validate v0.19.2 // indirect
	github.com/gogo/protobuf v1.2.0 // indirect
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/golang/mock v1.3.1
	github.com/google/btree v1.0.0 // indirect
	github.com/google/gofuzz v1.0.0 // indirect
	github.com/googleapis/gnostic v0.3.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway v1.9.4 // indirect
	github.com/json-iterator/go v1.1.6 // indirect
	github.com/liggitt/tabwriter v0.0.0-20181228230101-89fcab3d43de // indirect
	github.com/maxbrunsfeld/counterfeiter/v6 v6.2.1
	github.com/mitchellh/hashstructure v1.0.0
	github.com/munnerz/goautoneg v0.0.0-20190414153302-2ae31c8b6b30 // indirect
	github.com/openshift/api v0.0.0-00010101000000-000000000000
	github.com/openshift/client-go v0.0.0-00010101000000-000000000000
	github.com/operator-framework/operator-registry v1.1.1
	github.com/pkg/errors v0.8.1
	github.com/prometheus/client_golang v0.9.2
	github.com/sirupsen/logrus v1.4.2
	github.com/spf13/cobra v0.0.5
	github.com/stretchr/testify v1.3.0
	go.uber.org/atomic v1.4.0 // indirect
	go.uber.org/zap v1.10.0 // indirect
	golang.org/x/time v0.0.0-20190308202827-9d24e82272b4
	gonum.org/v1/gonum v0.0.0-20190710053202-4340aa3071a0 // indirect
	google.golang.org/grpc v1.22.0
	k8s.io/api v0.0.0-20190717022910-653c86b0609b
	k8s.io/apiextensions-apiserver v0.0.0-20181204003618-e419c5771cdc
	k8s.io/apimachinery v0.0.0-20190717022731-0bb8574e0887
	k8s.io/apiserver v0.0.0-20181026151315-13cfe3978170
	k8s.io/client-go v8.0.0+incompatible
	k8s.io/code-generator v0.0.0-20181203235156-f8cba74510f3
	k8s.io/component-base v0.0.0-20190717023551-b4f50308a616
	k8s.io/gengo v0.0.0-20190327210449-e17681d19d3a // indirect
	k8s.io/kube-aggregator v0.0.0-20181204002017-122bac39d429
	k8s.io/kube-openapi v0.0.0-20181031203759-72693cb1fadd
	k8s.io/kubernetes v1.11.8-beta.0.0.20190124204751-3a10094374f2
	k8s.io/utils v0.0.0-20190712204705-3dccf664f023 // indirect
	sigs.k8s.io/structured-merge-diff v0.0.0-00010101000000-000000000000 // indirect
	sigs.k8s.io/yaml v1.1.0 // indirect
)

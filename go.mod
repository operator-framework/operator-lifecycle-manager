module github.com/operator-framework/operator-lifecycle-manager

require (
	bitbucket.org/ww/goautoneg v0.0.0-20120707110453-75cd24fc2f2c // indirect
	github.com/NYTimes/gziphandler v1.0.1 // indirect
	github.com/PuerkitoBio/purell v1.1.0 // indirect
	github.com/PuerkitoBio/urlesc v0.0.0-20170810143723-de5bf2ad4578 // indirect
	github.com/asaskevich/govalidator v0.0.0-20180315120708-ccb8e960c48f // indirect
	github.com/beorn7/perks v0.0.0-20180321164747-3a771d992973 // indirect
	github.com/coreos/etcd v3.3.9+incompatible // indirect
	github.com/coreos/go-semver v0.2.0
	github.com/coreos/go-systemd v0.0.0-20180511133405-39ca1b05acc7 // indirect
	github.com/elazarl/go-bindata-assetfs v1.0.0 // indirect
	github.com/emicklei/go-restful v2.8.0+incompatible // indirect
	github.com/emicklei/go-restful-swagger12 v0.0.0-20170208215640-dcef7f557305 // indirect
	github.com/evanphx/json-patch v3.0.0+incompatible // indirect
	github.com/ghodss/yaml v1.0.0
	github.com/go-openapi/analysis v0.0.0-20180801175213-7c1bef8f6d9f // indirect
	github.com/go-openapi/errors v0.0.0-20180515155515-b2b2befaf267 // indirect
	github.com/go-openapi/jsonpointer v0.0.0-20180322222829-3a0015ad55fa // indirect
	github.com/go-openapi/jsonreference v0.0.0-20180322222742-3fb327e6747d // indirect
	github.com/go-openapi/loads v0.0.0-20171207192234-2a2b323bab96 // indirect
	github.com/go-openapi/runtime v0.0.0-20180628220156-9a3091f566c0 // indirect
	github.com/go-openapi/spec v0.0.0-20180801175345-384415f06ee2
	github.com/go-openapi/strfmt v0.0.0-20180703152050-913ee058e387 // indirect
	github.com/go-openapi/swag v0.0.0-20180715190254-becd2f08beaf // indirect
	github.com/go-openapi/validate v0.0.0-20180809073206-7c1911976134 // indirect
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/golang/mock v1.1.1
	github.com/hashicorp/golang-lru v0.5.0 // indirect
	github.com/mailru/easyjson v0.0.0-20180823135443-60711f1a8329 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.1 // indirect
	github.com/maxbrunsfeld/counterfeiter v0.0.0-20181017030959-1aadac120687 // indirect
	github.com/mitchellh/mapstructure v1.0.0 // indirect
	github.com/operator-framework/operator-registry v0.0.0-20181024172529-a172038c398d
	github.com/pborman/uuid v0.0.0-20170612153648-e790cca94e6c // indirect
	github.com/pkg/errors v0.8.0
	github.com/prometheus/client_golang v0.8.0
	github.com/prometheus/client_model v0.0.0-20180712105110-5c3871d89910 // indirect
	github.com/prometheus/common v0.0.0-20180801064454-c7de2306084e // indirect
	github.com/prometheus/procfs v0.0.0-20180725123919-05ee40e3a273 // indirect
	github.com/sirupsen/logrus v1.1.1
	github.com/spf13/cobra v0.0.3
	github.com/stretchr/testify v1.2.2
	github.com/ugorji/go v1.1.1 // indirect
	gopkg.in/mgo.v2 v2.0.0-20180705113604-9856a29383ce // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.0.0-20170531160350-a96e63847dc3 // indirect
	gopkg.in/yaml.v2 v2.2.1
	k8s.io/api v0.0.0-20180904230853-4e7be11eab3f
	k8s.io/apiextensions-apiserver v0.0.0-20180905004947-16750353bf97
	k8s.io/apimachinery v0.0.0-20180904193909-def12e63c512
	k8s.io/apiserver v0.0.0-20180904235525-d296c96c12b7
	k8s.io/client-go v0.0.0-20180718001006-59698c7d9724
	k8s.io/code-generator v0.0.0-20181026224033-5d042c2d6552 // indirect
	k8s.io/gengo v0.0.0-20180813235010-4242d8e6c5db // indirect
	k8s.io/kube-aggregator v0.0.0-20180905000155-efa32eb095fe
	k8s.io/kube-openapi v0.0.0-20181024003938-96e8bb74ecdd
	k8s.io/kubernetes v0.0.0-20180925111645-ae6d625c3a1c
)

replace github.com/operator-framework/operator-registry => ../operator-registry

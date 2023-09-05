package validatingroundtripper

import (
	"fmt"
	"net/http"
	"os"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/rest"
)

type validatingRoundTripper struct {
	delegate http.RoundTripper
}

func (rt *validatingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == "POST" {
		b, err := req.GetBody()
		if err != nil {
			panic(err)
		}
		dec := yaml.NewYAMLOrJSONDecoder(b, 10)
		unstructuredObject := &unstructured.Unstructured{}
		if err := dec.Decode(unstructuredObject); err != nil {
			panic(fmt.Errorf("error decoding object to an unstructured object: %w", err))
		}
		gvk := unstructuredObject.GroupVersionKind()
		if gvk.Kind != "Event" {
			if labels := unstructuredObject.GetLabels(); labels[install.OLMManagedLabelKey] != install.OLMManagedLabelValue {
				panic(fmt.Errorf("%s.%s/%v %s/%s does not have labels[%s]=%s", gvk.Kind, gvk.Group, gvk.Version, unstructuredObject.GetNamespace(), unstructuredObject.GetName(), install.OLMManagedLabelKey, install.OLMManagedLabelValue))
			}
		}
	}
	return rt.delegate.RoundTrip(req)
}

var _ http.RoundTripper = (*validatingRoundTripper)(nil)

// Wrap is meant to be used in developer environments and CI to make it easy to find places
// where we accidentally create Kubernetes objects without our management label.
func Wrap(cfg *rest.Config) *rest.Config {
	if _, set := os.LookupEnv("CI"); !set {
		return cfg
	}

	cfgCopy := *cfg
	cfgCopy.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return &validatingRoundTripper{delegate: rt}
	})
	return &cfgCopy
}

package validatingroundtripper

import (
	"fmt"
	"io"
	"net/http"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/rest"
)

type validatingRoundTripper struct {
	delegate http.RoundTripper
	codecs   serializer.CodecFactory
}

func (rt *validatingRoundTripper) decodeYAMLOrJSON(body io.Reader) (*unstructured.Unstructured, error) {
	dec := yaml.NewYAMLOrJSONDecoder(body, 10)
	unstructuredObject := &unstructured.Unstructured{}
	if err := dec.Decode(unstructuredObject); err != nil {
		return nil, fmt.Errorf("error decoding yaml/json object to an unstructured object: %w", err)
	}
	return unstructuredObject, nil
}

func (rt *validatingRoundTripper) decodeProtobuf(body io.Reader) (*unstructured.Unstructured, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}

	decoder := rt.codecs.UniversalDeserializer()
	obj, _, err := decoder.Decode(data, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decode protobuf data: %w", err)
	}

	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert object to unstructured: %w", err)
	}

	return &unstructured.Unstructured{Object: unstructuredObj}, nil
}

func (rt *validatingRoundTripper) decodeRequestBody(req *http.Request) (*unstructured.Unstructured, error) {
	b, err := req.GetBody()
	if err != nil {
		panic(fmt.Errorf("failed to get request body: %w", err))
	}
	defer b.Close()

	switch req.Header.Get("Content-Type") {
	case "application/vnd.kubernetes.protobuf":
		return rt.decodeProtobuf(b)
	default:
		return rt.decodeYAMLOrJSON(b)
	}
}

func (rt *validatingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == "POST" {
		unstructuredObject, err := rt.decodeRequestBody(req)

		if err != nil {
			return nil, err
		}

		gvk := unstructuredObject.GroupVersionKind()
		if gvk.Kind != "Event" {
			labels := unstructuredObject.GetLabels()
			if labels[install.OLMManagedLabelKey] != install.OLMManagedLabelValue {
				panic(fmt.Errorf("%s.%s/%v %s/%s does not have labels[%s]=%s",
					gvk.Kind, gvk.Group, gvk.Version,
					unstructuredObject.GetNamespace(), unstructuredObject.GetName(),
					install.OLMManagedLabelKey, install.OLMManagedLabelValue))
			}
		}
	}
	return rt.delegate.RoundTrip(req)
}

var _ http.RoundTripper = (*validatingRoundTripper)(nil)

// Wrap is meant to be used in developer environments and CI to make it easy to find places
// where we accidentally create Kubernetes objects without our management label.
func Wrap(cfg *rest.Config, scheme *runtime.Scheme) *rest.Config {
	if _, set := os.LookupEnv("CI"); !set {
		return cfg
	}

	cfgCopy := *cfg
	cfgCopy.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return &validatingRoundTripper{
			delegate: rt,
			codecs:   serializer.NewCodecFactory(scheme),
		}
	})
	return &cfgCopy
}

package scopedclient

import (
	"net/http"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/rest"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

// WarningRoundTripper implements the http RouterTripper interface and alerts via metrics if warning
// headers are present in the response, such as deprecation warnings (299). If the alert (via a metric) fails no error is returned
// as the RoundTrip interface should not return an error unless a transport level issue occurs.
type WarningRoundTripper struct{}

func SetWarningRoundTripper(config *rest.Config) *rest.Config {
	if config != nil {
		config.Transport = new(WarningRoundTripper)
	}
	return config
}

func (w *WarningRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// TODO make round tripper thread-safe
	rt := http.DefaultTransport
	res, err := rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	// inspect response for warnings on successful create
	if res.StatusCode == 201 {
		warnings, ok := res.Header["Warning"]
		if ok {
			u := &unstructured.Unstructured{}
			dec := yaml.NewYAMLOrJSONDecoder(res.Body, 10)
			if err := dec.Decode(u); err != nil {
				// mask error
				return res, nil
			}
			// send olm metric (best effort)
			for _, w := range warnings {
				// TODO remove extra quotes from header warning message
				metrics.EmitInstallPlanWarning(w, u.GetName(), u.GetNamespace(), u.GroupVersionKind().String())
			}
		}
	}

	return res, nil
}

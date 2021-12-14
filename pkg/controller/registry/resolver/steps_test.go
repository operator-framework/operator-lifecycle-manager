package resolver

import (
	"fmt"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/operator-framework/operator-registry/pkg/api"
)

const (
	optionalManifestProp = "{\"group\":\"monitoring.coreos.com\",\"kind\":\"PrometheusRule\",\"name\":\"myrule\"}"
)

func TestIsOptional(t *testing.T) {
	var (
		optionalManifestKey = manifestKey{
			Group: "monitoring.coreos.com",
			Kind:  "PrometheusRule",
			Name:  "myrule",
		}
		mandatoryManifestKey = manifestKey{
			Group: "authorization.openshift.io",
			Kind:  "ClusterRoleBinding",
			Name:  "mycrbinding",
		}
	)
	type isOptionalTests struct {
		resources []manifestKey
		results   []bool
	}
	log := logrus.New()
	properties := []*api.Property{
		{
			Type:  "olm.gvk",
			Value: "{\"group\":\"kibana.k8s.elastic.co\",\"kind\":\"Kibana\",\"version\":\"v1alpha1\"}",
		},
		{
			Type:  "olm.manifests.optional",
			Value: fmt.Sprintf(`{"manifests":[%s]}`, optionalManifestProp),
		},
		{
			Type:  "olm.gvk",
			Value: "{\"group\":\"apm.k8s.elastic.co\",\"kind\":\"ApmServer\",\"version\":\"v1beta1\"}",
		},
	}
	ioTests := isOptionalTests{
		resources: []manifestKey{optionalManifestKey, mandatoryManifestKey},
		results:   []bool{true, false},
	}
	isOptFunc := isOptional(properties, log)
	for i, resource := range ioTests.resources {
		result := isOptFunc(resource)
		if result != ioTests.results[i] {
			t.Errorf("resource: %s, got %t; want %t", resource, result, ioTests.results[i])
		}
	}
}

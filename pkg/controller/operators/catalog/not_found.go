package catalog

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

// gvkNotFoundError is returned from installplan execution when a step contains a GVK that is not found on cluster.
type gvkNotFoundError struct {
	gvk  schema.GroupVersionKind
	name string
}

func (g gvkNotFoundError) Error() string {
	return fmt.Sprintf("api-server resource not found installing %s %s: GroupVersionKind %s not found on the cluster. %s", g.gvk.Kind, g.name, g.gvk,
		"This API may have been deprecated and removed, see https://kubernetes.io/docs/reference/using-api/deprecation-guide/ for more information.")
}

type DiscoveryQuerier interface {
	QueryForGVK() error
}

type DiscoveryQuerierFunc func() error

func (d DiscoveryQuerierFunc) QueryForGVK() error {
	return d()
}

type discoveryQuerier struct {
	client discovery.DiscoveryInterface
}

func newDiscoveryQuerier(client discovery.DiscoveryInterface) *discoveryQuerier {
	return &discoveryQuerier{client: client}
}

// WithStepResource returns a DiscoveryQuerier which uses discovery to query for supported APIs on the server based on the provided step's GVK.
func (d *discoveryQuerier) WithStepResource(stepResource operatorsv1alpha1.StepResource) DiscoveryQuerier {
	var f DiscoveryQuerierFunc = func() error {
		gvk := schema.GroupVersionKind{Group: stepResource.Group, Version: stepResource.Version, Kind: stepResource.Kind}

		resourceList, err := d.client.ServerResourcesForGroupVersion(gvk.GroupVersion().String())
		if err != nil {
			if errors.IsNotFound(err) {
				return gvkNotFoundError{gvk: gvk, name: stepResource.Name}
			}
			return err
		}

		if resourceList == nil {
			return gvkNotFoundError{gvk: gvk, name: stepResource.Name}
		}

		for _, resource := range resourceList.APIResources {
			if resource.Kind == stepResource.Kind {
				// this kind is supported for this particular GroupVersion
				return nil
			}
		}

		return gvkNotFoundError{gvk: gvk, name: stepResource.Name}
	}
	return f
}

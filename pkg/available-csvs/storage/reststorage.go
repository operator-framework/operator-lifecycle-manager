package storage

import (
	"context"
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	genericreq "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/available-csvs/apis/available"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/available-csvs/apis/available/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/available-csvs/provider"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubernetes/pkg/printers"
	printerstorage "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubernetes/pkg/printers/storage"
)

type AvailableCSVStorage struct {
	groupResource schema.GroupResource
	prov          provider.Interface
	scheme        *runtime.Scheme
	rest.TableConvertor
}

var _ rest.Storage = &AvailableCSVStorage{}
var _ rest.KindProvider = &AvailableCSVStorage{}
var _ rest.Lister = &AvailableCSVStorage{}
var _ rest.Getter = &AvailableCSVStorage{}
var _ rest.Scoper = &AvailableCSVStorage{}
var _ rest.TableConvertor = &AvailableCSVStorage{}

// NewStorage returns a struct that implements methods needed for Kubernetes to satisfy API requests for the `AvailableClusterServieVersion` resource
func NewStorage(groupResource schema.GroupResource, prov provider.Interface, scheme *runtime.Scheme) *AvailableCSVStorage {
	return &AvailableCSVStorage{
		groupResource:  groupResource,
		prov:           prov,
		scheme:         scheme,
		TableConvertor: printerstorage.TableConvertor{TableGenerator: printers.NewTableGenerator().With(addTableHandlers)},
	}
}

// New satisfies the Storage interface
func (m *AvailableCSVStorage) New() runtime.Object {
	return &available.AvailableClusterServiceVersion{}
}

// Kind satisfies the KindProvider interface
func (m *AvailableCSVStorage) Kind() string {
	return v1alpha1.AvailableClusterServiceVersionKind
}

// NewList satisfies part of the Lister interface
func (m *AvailableCSVStorage) NewList() runtime.Object {
	return &available.AvailableClusterServiceVersionList{}
}

// List satisfies part of the Lister interface
func (m *AvailableCSVStorage) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	namespace := genericreq.NamespaceValue(ctx)

	labelSelector := labels.Everything()
	if options != nil && options.LabelSelector != nil {
		labelSelector = options.LabelSelector
	}

	name, err := nameFor(options.FieldSelector)
	if err != nil {
		return nil, err
	}

	res, err := m.prov.List(namespace, labelSelector)
	if err != nil {
		return nil, k8serrors.NewInternalError(err)
	}

	filtered := []available.AvailableClusterServiceVersion{}
	for _, manifest := range res.Items {
		if matches(manifest, name) {
			filtered = append(filtered, manifest)
		}
	}

	res.Items = filtered

	return res, nil
}

// Get satisfies the Getter interface
func (m *AvailableCSVStorage) Get(ctx context.Context, name string, opts *metav1.GetOptions) (runtime.Object, error) {
	return m.prov.Get(genericreq.NamespaceValue(ctx), name)
}

// NamespaceScoped satisfies the Scoper interface
func (m *AvailableCSVStorage) NamespaceScoped() bool {
	return true
}

func nameFor(fs fields.Selector) (string, error) {
	if fs == nil {
		fs = fields.Everything()
	}
	name := ""
	if value, found := fs.RequiresExactMatch("metadata.name"); found {
		name = value
	} else if !fs.Empty() {
		return "", fmt.Errorf("field label not supported: %s", fs.Requirements()[0].Field)
	}
	return name, nil
}

func matches(ac available.AvailableClusterServiceVersion, name string) bool {
	if name == "" {
		name = ac.GetName()
	}
	return ac.GetName() == name
}

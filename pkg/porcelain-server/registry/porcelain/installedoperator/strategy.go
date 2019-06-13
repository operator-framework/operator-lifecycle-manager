package installedoperator

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/apis/porcelain"
)

// NewStrategy creates and returns an installedStrategy instance
func NewStrategy(typer runtime.ObjectTyper) installedStrategy {
	return installedStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not an Installed
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*porcelain.InstalledOperator)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not an Installed")
	}
	return labels.Set(apiserver.ObjectMeta.Labels), SelectableFields(apiserver), nil
}

// MatchInstalled is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
func MatchInstalled(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *porcelain.InstalledOperator) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type installedStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (installedStrategy) NamespaceScoped() bool {
	return true
}

func (installedStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (installedStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (installedStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

func (installedStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (installedStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (installedStrategy) Canonicalize(obj runtime.Object) {
}

func (installedStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

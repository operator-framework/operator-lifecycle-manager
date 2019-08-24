package porcelain

import (
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/reference"

	operatorsinstall "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/install"
	operatorsv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
)

// TODO: Replace with constant from kubectl staging repo
const lastAppliedAnnotationKey = "kubectl.kubernetes.io/last-applied-configuration"

func init() {
	// Add required types to scheme
	addKnownTypes(scheme.Scheme)
	operatorsinstall.Install(scheme.Scheme)
}

type InstalledOperatorMeta interface {
	metav1.Object

	TargetsNamespace(namespace string) bool
	WithNamespace(namespace string)
	Sanitize()
}

type installedMeta struct {
	metav1.Object
}

func (i *installedMeta) TargetsNamespace(namespace string) bool {
	annotations := i.GetAnnotations()
	if len(annotations) == 0 {
		return false
	}

	key := operatorsv1.OperatorGroupTargetsAnnotationKey
	targets, ok := annotations[key]
	if !ok {
		return false
	}

	if targets == metav1.NamespaceAll {
		return true
	}

	for _, target := range strings.Split(targets, ",") {
		if target == namespace {
			return true
		}
	}

	return false
}

func (i *installedMeta) WithNamespace(namespace string) {
	i.SetUID(types.UID(fmt.Sprintf("%s/%s", namespace, i.GetUID())))
	i.SetNamespace(namespace)
}

func (i *installedMeta) Sanitize() {
	annotations := i.GetAnnotations()
	if len(annotations) == 0 {
		return
	}

	delete(annotations, operatorsv1.OperatorGroupTargetsAnnotationKey)
}

func InstalledOperatorMetaAccessor(obj interface{}) (InstalledOperatorMeta, error) {
	operator, ok := obj.(*InstalledOperator)
	if !ok {
		return nil, fmt.Errorf("obj is not of type installedoperator: %T", obj)
	}

	m := &installedMeta{
		Object: &operator.ObjectMeta,
	}

	return m, nil
}

type InstalledOperatorBuilder interface {
	Build() (*InstalledOperator, error)
	SetResourceVersionFromObject(obj runtime.Object) error
	SetClusterServiceVersion(csv *operatorsv1alpha1.ClusterServiceVersion) error
	SetSubscription(sub *operatorsv1alpha1.Subscription) error
}

type ioBuilder struct {
	io *InstalledOperator
}

func (ib *ioBuilder) Build() (*InstalledOperator, error) {
	// TODO: return an error if the InstalledOperator is missing any required fields
	return ib.io.DeepCopy(), nil
}

func (ib *ioBuilder) SetResourceVersionFromObject(obj runtime.Object) error {
	if ib.io == nil {
		ib.io = &InstalledOperator{}
	}

	io := &InstalledOperator{}
	if err := scheme.Scheme.Convert(obj, io, nil); err != nil {
		return err
	}

	ib.io.SetResourceVersion(io.GetResourceVersion())

	return nil
}

func (ib *ioBuilder) SetClusterServiceVersion(csv *operatorsv1alpha1.ClusterServiceVersion) error {
	if ib.io == nil {
		ib.io = &InstalledOperator{}
	}

	// CSV meta projection
	copiedCSV := csv.DeepCopy()
	ib.io.SetNamespace(csv.GetNamespace())
	ib.io.SetName(csv.GetName())
	ib.io.SetUID(csv.GetUID())
	ib.io.SetCreationTimestamp(csv.GetCreationTimestamp())
	ib.io.SetLabels(copiedCSV.GetLabels())

	annotations := copiedCSV.GetAnnotations()
	delete(annotations, lastAppliedAnnotationKey)
	ib.io.SetAnnotations(annotations)

	// Generate and add CSV reference
	csvRef, err := reference.GetReference(scheme.Scheme, csv)
	if err != nil {
		return err
	}
	ib.io.ClusterServiceVersionRef = csvRef

	// CSV spec projection
	ib.io.CustomResourceDefinitions = csv.Spec.CustomResourceDefinitions
	ib.io.APIServiceDefinitions = csv.Spec.APIServiceDefinitions
	ib.io.MinKubeVersion = csv.Spec.MinKubeVersion
	ib.io.Version = csv.Spec.Version
	ib.io.Maturity = csv.Spec.Maturity
	ib.io.DisplayName = csv.Spec.DisplayName
	ib.io.Description = csv.Spec.Description
	ib.io.Keywords = csv.Spec.Keywords
	ib.io.Maintainers = csv.Spec.Maintainers
	ib.io.Provider = csv.Spec.Provider
	ib.io.Links = csv.Spec.Links
	ib.io.Icon = csv.Spec.Icon
	ib.io.InstallModes = csv.Spec.InstallModes
	ib.io.Replaces = csv.Spec.Replaces

	// CSV status projection
	ib.io.Phase = csv.Status.Phase
	ib.io.Message = csv.Status.Message
	ib.io.Reason = csv.Status.Reason

	return nil
}

func (ib *ioBuilder) SetSubscription(sub *operatorsv1alpha1.Subscription) error {
	if ib.io == nil {
		ib.io = &InstalledOperator{}
	}

	if sub == nil {
		return nil
	}

	subRef, err := reference.GetReference(scheme.Scheme, sub)
	if err != nil {
		return err
	}

	ib.io.SubscriptionRef = subRef

	if sub.Spec != nil {
		ib.io.CatalogSourceName = sub.Spec.CatalogSource
		ib.io.CatalogSourceNamespace = sub.Spec.CatalogSourceNamespace
		ib.io.Package = sub.Spec.Package
		ib.io.Channel = sub.Spec.Channel
	}

	return nil
}

func NewInstalledOperatorBuilder() InstalledOperatorBuilder {
	return &ioBuilder{
		io: &InstalledOperator{},
	}
}

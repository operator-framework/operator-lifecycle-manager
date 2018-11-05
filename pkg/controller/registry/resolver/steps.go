package resolver

import (
	"bytes"
	"fmt"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	extScheme "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/scheme"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sjson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	k8sscheme.AddToScheme(scheme)
	extScheme.AddToScheme(scheme)
	v1alpha1.AddToScheme(scheme)
}

// NewStepResourceFromCSV creates an unresolved Step for the provided CSV.
func NewStepResourceFromCSV(csv *v1alpha1.ClusterServiceVersion) (v1alpha1.StepResource, error) {
	return NewStepResourceFromObject(csv, csv.GetName())
}

// NewStepResourceFromCRD creates an unresolved Step for the provided CRD.
func NewStepResourcesFromCRD(crd *v1beta1.CustomResourceDefinition) ([]v1alpha1.StepResource, error) {
	steps := []v1alpha1.StepResource{}

	crdStep, err := NewStepResourceFromObject(crd, crd.GetName())
	if err != nil {
		return nil, err
	}
	steps = append(steps, crdStep)

	editRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("edit-%s-%s", crd.Name, crd.Spec.Version),
			Labels: map[string]string{
				"rbac.authorization.k8s.io/aggregate-to-admin": "true",
				"rbac.authorization.k8s.io/aggregate-to-edit":  "true",
			},
		},
		Rules: []rbacv1.PolicyRule{{Verbs: []string{"create", "update", "patch", "delete"}, APIGroups: []string{crd.Spec.Group}, Resources: []string{crd.Spec.Names.Plural}}},
	}
	editRoleStep, err := NewStepResourceFromObject(editRole, editRole.GetName())
	if err != nil {
		return nil, err
	}
	steps = append(steps, editRoleStep)

	viewRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("view-%s-%s", crd.Name, crd.Spec.Version),
			Labels: map[string]string{
				"rbac.authorization.k8s.io/aggregate-to-view": "true",
			},
		},
		Rules: []rbacv1.PolicyRule{
			{Verbs: []string{"get", "list", "watch"}, APIGroups: []string{crd.Spec.Group}, Resources: []string{crd.Spec.Names.Plural}},
			{Verbs: []string{"get", "watch"}, APIGroups: []string{v1beta1.GroupName}, Resources: []string{crd.GetName()}},
		},
	}
	viewRoleStep, err := NewStepResourceFromObject(viewRole, viewRole.GetName())
	if err != nil {
		return nil, err
	}
	steps = append(steps, viewRoleStep)

	return steps, nil
}

// NewStepResourceForObject returns a new StepResource for the provided object
func NewStepResourceFromObject(obj runtime.Object, name string) (v1alpha1.StepResource, error) {
	var resource v1alpha1.StepResource

	// set up object serializer
	serializer := k8sjson.NewSerializer(k8sjson.DefaultMetaFactory, scheme, scheme, true)

	// create an object manifest
	var manifest bytes.Buffer
	err := serializer.Encode(obj, &manifest)
	if err != nil {
		return resource, err
	}

	if err := ownerutil.InferGroupVersionKind(obj); err != nil {
		return resource, err
	}

	gvk := obj.GetObjectKind().GroupVersionKind()

	// create the resource
	resource = v1alpha1.StepResource{
		Name:     name,
		Kind:     gvk.Kind,
		Group:    gvk.Group,
		Version:  gvk.Version,
		Manifest: manifest.String(),
	}

	return resource, nil
}

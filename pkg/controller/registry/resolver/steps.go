package resolver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/operator-framework/operator-registry/pkg/api"
	extScheme "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/scheme"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8sjson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/projection"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

const (
	secretKind       = "Secret"
	BundleSecretKind = "BundleSecret"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(k8sscheme.AddToScheme(scheme))
	utilruntime.Must(extScheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

// NewStepResourceForObject returns a new StepResource for the provided object
func NewStepResourceFromObject(obj runtime.Object, catalogSourceName, catalogSourceNamespace string) (v1alpha1.StepResource, error) {
	var resource v1alpha1.StepResource

	// set up object serializer
	serializer := k8sjson.NewSerializer(k8sjson.DefaultMetaFactory, scheme, scheme, false)

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

	metaObj, ok := obj.(metav1.Object)
	if !ok {
		return resource, fmt.Errorf("couldn't get object metadata")
	}

	name := metaObj.GetName()
	if name == "" {
		name = metaObj.GetGenerateName()
	}

	// create the resource
	resource = v1alpha1.StepResource{
		Name:                   name,
		Kind:                   gvk.Kind,
		Group:                  gvk.Group,
		Version:                gvk.Version,
		Manifest:               manifest.String(),
		CatalogSource:          catalogSourceName,
		CatalogSourceNamespace: catalogSourceNamespace,
	}

	// BundleSecret is a synthetic kind that OLM uses to distinguish between secrets included in the bundle and
	// pull secrets included in the installplan
	if obj.GetObjectKind().GroupVersionKind().Kind == secretKind {
		resource.Kind = BundleSecretKind
	}

	return resource, nil
}

func NewSubscriptionStepResource(namespace string, info OperatorSourceInfo) (v1alpha1.StepResource, error) {
	return NewStepResourceFromObject(&v1alpha1.Subscription{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      strings.Join([]string{info.Package, info.Channel, info.Catalog.Name, info.Catalog.Namespace}, "-"),
		},
		Spec: &v1alpha1.SubscriptionSpec{
			CatalogSource:          info.Catalog.Name,
			CatalogSourceNamespace: info.Catalog.Namespace,
			Package:                info.Package,
			Channel:                info.Channel,
			StartingCSV:            info.StartingCSV,
			InstallPlanApproval:    v1alpha1.ApprovalAutomatic,
		},
	}, info.Catalog.Name, info.Catalog.Namespace)
}

func V1alpha1CSVFromBundle(bundle *api.Bundle) (*v1alpha1.ClusterServiceVersion, error) {
	csv := &v1alpha1.ClusterServiceVersion{}
	if err := json.Unmarshal([]byte(bundle.CsvJson), csv); err != nil {
		return nil, err
	}
	return csv, nil
}

func NewStepResourceFromBundle(bundle *api.Bundle, namespace, replaces, catalogSourceName, catalogSourceNamespace string) ([]v1alpha1.StepResource, error) {
	csv, err := V1alpha1CSVFromBundle(bundle)
	if err != nil {
		return nil, err
	}

	csv.SetNamespace(namespace)
	csv.Spec.Replaces = replaces
	if anno, err := projection.PropertiesAnnotationFromPropertyList(bundle.Properties); err != nil {
		return nil, fmt.Errorf("failed to construct properties annotation for %q: %w", csv.GetName(), err)
	} else {
		annos := csv.GetAnnotations()
		if annos == nil {
			annos = make(map[string]string)
		}
		annos[projection.PropertiesAnnotationKey] = anno
		csv.SetAnnotations(annos)
	}

	step, err := NewStepResourceFromObject(csv, catalogSourceName, catalogSourceNamespace)
	if err != nil {
		return nil, err
	}
	steps := []v1alpha1.StepResource{step}

	for _, object := range bundle.Object {
		dec := yaml.NewYAMLOrJSONDecoder(strings.NewReader(object), 10)
		unst := &unstructured.Unstructured{}
		if err := dec.Decode(unst); err != nil {
			return nil, err
		}

		if unst.GetObjectKind().GroupVersionKind().Kind == v1alpha1.ClusterServiceVersionKind {
			continue
		}

		step, err := NewStepResourceFromObject(unst, catalogSourceName, catalogSourceNamespace)
		if err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}

	operatorServiceAccountSteps, err := NewServiceAccountStepResources(csv, catalogSourceName, catalogSourceNamespace)
	if err != nil {
		return nil, err
	}
	steps = append(steps, operatorServiceAccountSteps...)
	return steps, nil
}

func NewStepsFromBundle(bundle *api.Bundle, namespace, replaces, catalogSourceName, catalogSourceNamespace string) ([]*v1alpha1.Step, error) {
	bundleSteps, err := NewStepResourceFromBundle(bundle, namespace, replaces, catalogSourceName, catalogSourceNamespace)
	if err != nil {
		return nil, err
	}

	var steps []*v1alpha1.Step
	for _, s := range bundleSteps {
		steps = append(steps, &v1alpha1.Step{
			Resolving: bundle.CsvName,
			Resource:  s,
			Status:    v1alpha1.StepStatusUnknown,
		})
	}

	return steps, nil
}

// NewServiceAccountStepResources returns a list of step resources required to satisfy the RBAC requirements of the given CSV's InstallStrategy
func NewServiceAccountStepResources(csv *v1alpha1.ClusterServiceVersion, catalogSourceName, catalogSourceNamespace string) ([]v1alpha1.StepResource, error) {
	var rbacSteps []v1alpha1.StepResource

	operatorPermissions, err := RBACForClusterServiceVersion(csv)
	if err != nil {
		return nil, err
	}

	for _, perms := range operatorPermissions {
		step, err := NewStepResourceFromObject(perms.ServiceAccount, catalogSourceName, catalogSourceNamespace)
		if err != nil {
			return nil, err
		}
		rbacSteps = append(rbacSteps, step)
		for _, role := range perms.Roles {
			step, err := NewStepResourceFromObject(role, catalogSourceName, catalogSourceNamespace)
			if err != nil {
				return nil, err
			}
			rbacSteps = append(rbacSteps, step)
		}
		for _, roleBinding := range perms.RoleBindings {
			step, err := NewStepResourceFromObject(roleBinding, catalogSourceName, catalogSourceNamespace)
			if err != nil {
				return nil, err
			}
			rbacSteps = append(rbacSteps, step)
		}
		for _, clusterRole := range perms.ClusterRoles {
			step, err := NewStepResourceFromObject(clusterRole, catalogSourceName, catalogSourceNamespace)
			if err != nil {
				return nil, err
			}
			rbacSteps = append(rbacSteps, step)
		}
		for _, clusterRoleBinding := range perms.ClusterRoleBindings {
			step, err := NewStepResourceFromObject(clusterRoleBinding, catalogSourceName, catalogSourceNamespace)
			if err != nil {
				return nil, err
			}
			rbacSteps = append(rbacSteps, step)
		}
	}
	return rbacSteps, nil
}

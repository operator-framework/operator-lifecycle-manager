package convert

import (
	"fmt"
	"hash"
	"hash/fnv"
	"strings"

	"github.com/davecgh/go-spew/spew"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type RegistryV1 struct {
	CSV    v1alpha1.ClusterServiceVersion
	CRDs   []apiextensionsv1.CustomResourceDefinition
	Others []unstructured.Unstructured
}

type Plain struct {
	Objects []client.Object
}

func validateTargetNamespaces(supportedInstallModes sets.String, installNamespace string, targetNamespaces []string) error {
	set := sets.NewString(targetNamespaces...)
	switch set.Len() {
	case 0:
		if supportedInstallModes.Has(string(v1alpha1.InstallModeTypeAllNamespaces)) {
			return nil
		}
	case 1:
		if set.Has("") && supportedInstallModes.Has(string(v1alpha1.InstallModeTypeAllNamespaces)) {
			return nil
		}
		if supportedInstallModes.Has(string(v1alpha1.InstallModeTypeSingleNamespace)) {
			return nil
		}
		if supportedInstallModes.Has(string(v1alpha1.InstallModeTypeOwnNamespace)) && targetNamespaces[0] == installNamespace {
			return nil
		}
	default:
		if supportedInstallModes.Has(string(v1alpha1.InstallModeTypeMultiNamespace)) {
			return nil
		}
	}
	return fmt.Errorf("supported install modes %v do not support target namespaces %v", supportedInstallModes.List(), targetNamespaces)
}

func Convert(in RegistryV1, installNamespace string, targetNamespaces []string) (*Plain, error) {
	if installNamespace == "" {
		installNamespace = in.CSV.Annotations["operatorframework.io/suggested-namespace"]
	}
	supportedInstallModes := sets.NewString()
	for _, im := range in.CSV.Spec.InstallModes {
		if im.Supported {
			supportedInstallModes.Insert(string(im.Type))
		}
	}
	if targetNamespaces == nil {
		if supportedInstallModes.Has(string(v1alpha1.InstallModeTypeAllNamespaces)) {
			targetNamespaces = []string{}
		} else if supportedInstallModes.Has(string(v1alpha1.InstallModeTypeOwnNamespace)) {
			targetNamespaces = []string{installNamespace}
		}
	}

	if err := validateTargetNamespaces(supportedInstallModes, installNamespace, targetNamespaces); err != nil {
		return nil, err
	}

	deployments := []appsv1.Deployment{}
	serviceAccounts := map[string]corev1.ServiceAccount{}
	for _, depSpec := range in.CSV.Spec.InstallStrategy.StrategySpec.DeploymentSpecs {
		annotations := in.CSV.Annotations
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations["olm.targetNamespaces"] = strings.Join(targetNamespaces, ",")
		deployments = append(deployments, appsv1.Deployment{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Deployment",
				APIVersion: appsv1.SchemeGroupVersion.String(),
			},

			ObjectMeta: metav1.ObjectMeta{
				Namespace:   installNamespace,
				Name:        depSpec.Name,
				Labels:      depSpec.Label,
				Annotations: annotations,
			},
			Spec: depSpec.Spec,
		})
		serviceAccounts[depSpec.Spec.Template.Spec.ServiceAccountName] = corev1.ServiceAccount{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ServiceAccount",
				APIVersion: corev1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: installNamespace,
				Name:      depSpec.Spec.Template.Spec.ServiceAccountName,
			},
		}
	}
	var (
		roles               []rbacv1.Role
		roleBindings        []rbacv1.RoleBinding
		clusterRoles        []rbacv1.ClusterRole
		clusterRoleBindings []rbacv1.ClusterRoleBinding
	)
	for _, ns := range targetNamespaces {
		if ns == "" {
			continue
		}
		for _, permission := range in.CSV.Spec.InstallStrategy.StrategySpec.Permissions {
			if _, ok := serviceAccounts[permission.ServiceAccountName]; !ok {
				serviceAccounts[permission.ServiceAccountName] = corev1.ServiceAccount{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ServiceAccount",
						APIVersion: corev1.SchemeGroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: installNamespace,
						Name:      permission.ServiceAccountName,
					},
				}
			}
			name := generateName(fmt.Sprintf("%s-%s", in.CSV.GetName(), permission.ServiceAccountName), []interface{}{in.CSV.GetName(), permission})
			roles = append(roles, rbacv1.Role{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Role",
					APIVersion: rbacv1.SchemeGroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: ns,
					Name:      name,
				},
				Rules: permission.Rules,
			})
			roleBindings = append(roleBindings, rbacv1.RoleBinding{
				TypeMeta: metav1.TypeMeta{
					Kind:       "RoleBinding",
					APIVersion: rbacv1.SchemeGroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: ns,
					Name:      name,
				},
				Subjects: []rbacv1.Subject{
					{
						Kind:      "ServiceAccount",
						Name:      permission.ServiceAccountName,
						Namespace: installNamespace,
					},
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: rbacv1.GroupName,
					Kind:     "Role",
					Name:     name,
				},
			})
		}
	}
	for _, permission := range in.CSV.Spec.InstallStrategy.StrategySpec.ClusterPermissions {
		if _, ok := serviceAccounts[permission.ServiceAccountName]; !ok {
			serviceAccounts[permission.ServiceAccountName] = corev1.ServiceAccount{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ServiceAccount",
					APIVersion: corev1.SchemeGroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: installNamespace,
					Name:      permission.ServiceAccountName,
				},
			}
		}
		name := generateName(fmt.Sprintf("%s-%s", in.CSV.GetName(), permission.ServiceAccountName), []interface{}{in.CSV.GetName(), permission})
		clusterRoles = append(clusterRoles, rbacv1.ClusterRole{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ClusterRole",
				APIVersion: rbacv1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Rules: permission.Rules,
		})
		clusterRoleBindings = append(clusterRoleBindings, rbacv1.ClusterRoleBinding{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ClusterRoleBinding",
				APIVersion: rbacv1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      permission.ServiceAccountName,
					Namespace: installNamespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     name,
			},
		})
	}

	var objs []client.Object
	for _, obj := range serviceAccounts {
		obj := obj
		objs = append(objs, &obj)
	}
	for _, obj := range roles {
		obj := obj
		objs = append(objs, &obj)
	}
	for _, obj := range roleBindings {
		obj := obj
		objs = append(objs, &obj)
	}
	for _, obj := range clusterRoles {
		obj := obj
		objs = append(objs, &obj)
	}
	for _, obj := range clusterRoleBindings {
		obj := obj
		objs = append(objs, &obj)
	}
	for _, obj := range in.CRDs {
		obj := obj
		objs = append(objs, &obj)
	}
	for _, obj := range in.Others {
		obj := obj
		objs = append(objs, &obj)
	}
	for _, obj := range deployments {
		obj := obj
		objs = append(objs, &obj)
	}
	return &Plain{Objects: objs}, nil
}

const maxNameLength = 63

func generateName(base string, o interface{}) string {
	hasher := fnv.New32a()

	deepHashObject(hasher, o)
	hashStr := rand.SafeEncodeString(fmt.Sprint(hasher.Sum32()))
	if len(base)+len(hashStr) > maxNameLength {
		base = base[:maxNameLength-len(hashStr)-1]
	}

	return fmt.Sprintf("%s-%s", base, hashStr)
}

// deepHashObject writes specified object to hash using the spew library
// which follows pointers and prints actual values of the nested objects
// ensuring the hash does not change when a pointer changes.
func deepHashObject(hasher hash.Hash, objectToWrite interface{}) {
	hasher.Reset()
	printer := spew.ConfigState{
		Indent:         " ",
		SortKeys:       true,
		DisableMethods: true,
		SpewKeys:       true,
	}
	if _, err := printer.Fprintf(hasher, "%#v", objectToWrite); err != nil {
		panic(err)
	}
}

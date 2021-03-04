package e2e

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

var _ = Describe("ClusterServiceVersion", func() {
	var (
		c   operatorclient.ClientInterface
		crc versioned.Interface
	)
	BeforeEach(func() {

		c = newKubeClient()
		crc = newCRClient()
	})
	AfterEach(func() {
		TearDown(testNamespace)
	})

	When("a copied csv exists", func() {
		var (
			target   corev1.Namespace
			original v1alpha1.ClusterServiceVersion
			copy     v1alpha1.ClusterServiceVersion
		)

		BeforeEach(func() {
			target = corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "watched-",
				},
			}
			Expect(ctx.Ctx().Client().Create(context.Background(), &target)).To(Succeed())

			original = v1alpha1.ClusterServiceVersion{
				TypeMeta: metav1.TypeMeta{
					Kind:       v1alpha1.ClusterServiceVersionKind,
					APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
				},
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "csv-",
					Namespace:    testNamespace,
				},
				Spec: v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: newNginxInstallStrategy(genName("csv-"), nil, nil),
					InstallModes: []v1alpha1.InstallMode{
						{
							Type:      v1alpha1.InstallModeTypeAllNamespaces,
							Supported: true,
						},
					},
				},
			}
			Expect(ctx.Ctx().Client().Create(context.Background(), &original)).To(Succeed())

			Eventually(func() error {
				key := client.ObjectKeyFromObject(&original)
				key.Namespace = target.GetName()
				return ctx.Ctx().Client().Get(context.Background(), key, &copy)
			}).Should(Succeed())
		})

		AfterEach(func() {
			if target.GetName() != "" {
				Expect(ctx.Ctx().Client().Delete(context.Background(), &target)).To(Succeed())
			}
		})

		It("is synchronized with the original csv", func() {
			Eventually(func() error {
				key := client.ObjectKeyFromObject(&copy)

				key.Namespace = target.Name
				if err := ctx.Ctx().Client().Get(context.Background(), key, &copy); err != nil {
					return err
				}

				copy.Status.LastUpdateTime = &metav1.Time{Time: time.Unix(1, 0)}
				return ctx.Ctx().Client().Status().Update(context.Background(), &copy)
			}).Should(Succeed())

			Eventually(func() (bool, error) {
				key := client.ObjectKeyFromObject(&original)

				if err := ctx.Ctx().Client().Get(context.Background(), key, &original); err != nil {
					return false, err
				}

				key.Namespace = target.Name
				if err := ctx.Ctx().Client().Get(context.Background(), key, &copy); err != nil {
					return false, err
				}

				return original.Status.LastUpdateTime.Equal(copy.Status.LastUpdateTime), nil
			}).Should(BeTrue(), "Change to status of copy should have been reverted")
		})
	})

	When("a csv requires a serviceaccount solely owned by a non-csv", func() {
		var (
			cm  corev1.ConfigMap
			sa  corev1.ServiceAccount
			csv v1alpha1.ClusterServiceVersion
		)

		BeforeEach(func() {
			cm = corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "cm-",
					Namespace:    testNamespace,
				},
			}
			Expect(ctx.Ctx().Client().Create(context.Background(), &cm)).To(Succeed())

			sa = corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "sa-",
					Namespace:    testNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							Name:       cm.GetName(),
							APIVersion: corev1.SchemeGroupVersion.String(),
							Kind:       "ConfigMap",
							UID:        cm.GetUID(),
						},
					},
				},
			}
			Expect(ctx.Ctx().Client().Create(context.Background(), &sa)).To(Succeed())

			csv = v1alpha1.ClusterServiceVersion{
				TypeMeta: metav1.TypeMeta{
					Kind:       v1alpha1.ClusterServiceVersionKind,
					APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
				},
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "csv-",
					Namespace:    testNamespace,
				},
				Spec: v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName: v1alpha1.InstallStrategyNameDeployment,
						StrategySpec: v1alpha1.StrategyDetailsDeployment{
							DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
								{
									Name: "foo",
									Spec: appsv1.DeploymentSpec{
										Selector: &metav1.LabelSelector{
											MatchLabels: map[string]string{"app": "foo"},
										},
										Template: corev1.PodTemplateSpec{
											ObjectMeta: metav1.ObjectMeta{
												Labels: map[string]string{"app": "foo"},
											},
											Spec: corev1.PodSpec{Containers: []corev1.Container{
												{
													Name:  genName("foo"),
													Image: *dummyImage,
												},
											}},
										},
									},
								},
							},
							Permissions: []v1alpha1.StrategyDeploymentPermissions{
								{
									ServiceAccountName: sa.GetName(),
									Rules:              []rbacv1.PolicyRule{},
								},
							},
						},
					},
					InstallModes: []v1alpha1.InstallMode{
						{
							Type:      v1alpha1.InstallModeTypeAllNamespaces,
							Supported: true,
						},
					},
				},
			}
			Expect(ctx.Ctx().Client().Create(context.Background(), &csv)).To(Succeed())
		})

		AfterEach(func() {
			if cm.GetName() != "" {
				Expect(ctx.Ctx().Client().Delete(context.Background(), &cm)).To(Succeed())
			}
		})

		It("considers the serviceaccount requirement satisfied", func() {
			Eventually(func() (v1alpha1.StatusReason, error) {
				if err := ctx.Ctx().Client().Get(context.Background(), client.ObjectKeyFromObject(&csv), &csv); err != nil {
					return "", err
				}
				for _, requirement := range csv.Status.RequirementStatus {
					if requirement.Name != sa.GetName() {
						continue
					}
					return requirement.Status, nil
				}
				return "", fmt.Errorf("missing expected requirement %q", sa.GetName())
			}).Should(Equal(v1alpha1.RequirementStatusReasonPresent))
		})
	})

	It("create with unmet requirements min kube version", func() {

		depName := genName("dep-")
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "999.999.999",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: newNginxInstallStrategy(depName, nil, nil),
			},
		}

		cleanupCSV, err := createCSV(c, crc, csv, testNamespace, false, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCSV()

		_, err = fetchCSV(crc, csv.Name, testNamespace, csvPendingChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Shouldn't create deployment
		Consistently(func() bool {
			_, err := c.GetDeployment(testNamespace, depName)
			return k8serrors.IsNotFound(err)
		}).Should(BeTrue())
	})
	// TODO: same test but missing serviceaccount instead
	It("create with unmet requirements CRD", func() {

		depName := genName("dep-")
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: newNginxInstallStrategy(depName, nil, nil),
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							DisplayName: "Not In Cluster",
							Description: "A CRD that is not currently in the cluster",
							Name:        "not.in.cluster.com",
							Version:     "v1alpha1",
							Kind:        "NotInCluster",
						},
					},
				},
			},
		}

		cleanupCSV, err := createCSV(c, crc, csv, testNamespace, false, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCSV()

		_, err = fetchCSV(crc, csv.Name, testNamespace, csvPendingChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Shouldn't create deployment
		Consistently(func() bool {
			_, err := c.GetDeployment(testNamespace, depName)
			return k8serrors.IsNotFound(err)
		}).Should(BeTrue())
	})

	It("create with unmet permissions CRD", func() {

		saName := genName("dep-")
		permissions := []v1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: saName,
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"create"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
				},
			},
		}

		clusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: saName,
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"get"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
				},
			},
		}

		crdPlural := genName("ins")
		crdName := crdPlural + ".cluster.com"
		depName := genName("dep-")
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: newNginxInstallStrategy(depName, permissions, clusterPermissions),
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: crdName,
						},
					},
				},
			},
		}

		// Create dependency first (CRD)
		cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group:   "cluster.com",
				Version: "v1alpha1",
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		})
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCRD()

		cleanupCSV, err := createCSV(c, crc, csv, testNamespace, true, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCSV()

		_, err = fetchCSV(crc, csv.Name, testNamespace, csvPendingChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Shouldn't create deployment
		Consistently(func() bool {
			_, err := c.GetDeployment(testNamespace, depName)
			return k8serrors.IsNotFound(err)
		}).Should(BeTrue())
	})
	It("create with unmet requirements API service", func() {

		depName := genName("dep-")
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: newNginxInstallStrategy(depName, nil, nil),
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Required: []v1alpha1.APIServiceDescription{
						{
							DisplayName: "Not In Cluster",
							Description: "An apiservice that is not currently in the cluster",
							Group:       "not.in.cluster.com",
							Version:     "v1alpha1",
							Kind:        "NotInCluster",
						},
					},
				},
			},
		}

		cleanupCSV, err := createCSV(c, crc, csv, testNamespace, false, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCSV()

		_, err = fetchCSV(crc, csv.Name, testNamespace, csvPendingChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Shouldn't create deployment
		Consistently(func() bool {
			_, err := c.GetDeployment(testNamespace, depName)
			return k8serrors.IsNotFound(err)
		}).Should(BeTrue())
	})
	It("create with unmet permissions API service", func() {

		saName := genName("dep-")
		permissions := []v1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: saName,
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"create"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
				},
			},
		}

		clusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: saName,
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"get"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
				},
			},
		}

		depName := genName("dep-")
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: newNginxInstallStrategy(depName, permissions, clusterPermissions),
				// Cheating a little; this is an APIservice that will exist for the e2e tests
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Required: []v1alpha1.APIServiceDescription{
						{
							Group:       "packages.operators.coreos.com",
							Version:     "v1",
							Kind:        "PackageManifest",
							DisplayName: "Package Manifest",
							Description: "An apiservice that exists",
						},
					},
				},
			},
		}

		cleanupCSV, err := createCSV(c, crc, csv, testNamespace, false, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCSV()

		_, err = fetchCSV(crc, csv.Name, testNamespace, csvPendingChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Shouldn't create deployment
		Consistently(func() bool {
			_, err := c.GetDeployment(testNamespace, depName)
			return k8serrors.IsNotFound(err)
		}).Should(BeTrue())
	})
	It("create with unmet requirements native API", func() {

		depName := genName("dep-")
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: newNginxInstallStrategy(depName, nil, nil),
				NativeAPIs:      []metav1.GroupVersionKind{{Group: "kubenative.io", Version: "v1", Kind: "Native"}},
			},
		}

		cleanupCSV, err := createCSV(c, crc, csv, testNamespace, false, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCSV()

		_, err = fetchCSV(crc, csv.Name, testNamespace, csvPendingChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Shouldn't create deployment
		Consistently(func() bool {
			_, err := c.GetDeployment(testNamespace, depName)
			return k8serrors.IsNotFound(err)
		}).Should(BeTrue())
	})
	// TODO: same test but create serviceaccount instead
	It("create requirements met CRD", func() {

		saName := genName("sa-")
		permissions := []v1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: saName,
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"create"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
				},
			},
		}

		clusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: saName,
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"get"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
					{
						Verbs:           []string{"put", "post", "get"},
						NonResourceURLs: []string{"/osb", "/osb/*"},
					},
				},
			},
		}

		crdPlural := genName("ins")
		crdName := crdPlural + ".cluster.com"
		depName := genName("dep-")
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: newNginxInstallStrategy(depName, permissions, clusterPermissions),
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: crdName,
						},
					},
				},
			},
		}

		// Create CSV first, knowing it will fail
		cleanupCSV, err := createCSV(c, crc, csv, testNamespace, true, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCSV()

		fetchedCSV, err := fetchCSV(crc, csv.Name, testNamespace, csvPendingChecker)
		Expect(err).ShouldNot(HaveOccurred())

		sa := corev1.ServiceAccount{}
		sa.SetName(saName)
		sa.SetNamespace(testNamespace)
		sa.SetOwnerReferences([]metav1.OwnerReference{{
			Name:       fetchedCSV.GetName(),
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			Kind:       v1alpha1.ClusterServiceVersionKind,
			UID:        fetchedCSV.GetUID(),
		}})
		_, err = c.CreateServiceAccount(&sa)
		Expect(err).ShouldNot(HaveOccurred(), "could not create ServiceAccount %#v", sa)

		crd := apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group:   "cluster.com",
				Version: "v1alpha1",
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		}
		crd.SetOwnerReferences([]metav1.OwnerReference{{
			Name:       fetchedCSV.GetName(),
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			Kind:       v1alpha1.ClusterServiceVersionKind,
			UID:        fetchedCSV.GetUID(),
		}})
		cleanupCRD, err := createCRD(c, crd)
		defer cleanupCRD()
		Expect(err).ShouldNot(HaveOccurred())

		// Create Role/Cluster Roles and RoleBindings
		role := rbacv1.Role{
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"create"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		}
		role.SetName(genName("dep-"))
		role.SetNamespace(testNamespace)
		_, err = c.CreateRole(&role)
		Expect(err).ShouldNot(HaveOccurred(), "could not create Role")

		roleBinding := rbacv1.RoleBinding{
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					APIGroup:  "",
					Name:      sa.GetName(),
					Namespace: sa.GetNamespace(),
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     role.GetName(),
			},
		}
		roleBinding.SetName(genName("dep-"))
		roleBinding.SetNamespace(testNamespace)
		_, err = c.CreateRoleBinding(&roleBinding)
		Expect(err).ShouldNot(HaveOccurred(), "could not create RoleBinding")

		clusterRole := rbacv1.ClusterRole{
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		}
		clusterRole.SetName(genName("dep-"))
		_, err = c.CreateClusterRole(&clusterRole)
		Expect(err).ShouldNot(HaveOccurred(), "could not create ClusterRole")

		nonResourceClusterRole := rbacv1.ClusterRole{
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:           []string{"put", "post", "get"},
					NonResourceURLs: []string{"/osb", "/osb/*"},
				},
			},
		}
		nonResourceClusterRole.SetName(genName("dep-"))
		_, err = c.CreateClusterRole(&nonResourceClusterRole)
		Expect(err).ShouldNot(HaveOccurred(), "could not create ClusterRole")

		clusterRoleBinding := rbacv1.ClusterRoleBinding{
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					APIGroup:  "",
					Name:      sa.GetName(),
					Namespace: sa.GetNamespace(),
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     clusterRole.GetName(),
			},
		}
		clusterRoleBinding.SetName(genName("dep-"))
		_, err = c.CreateClusterRoleBinding(&clusterRoleBinding)
		Expect(err).ShouldNot(HaveOccurred(), "could not create ClusterRoleBinding")

		nonResourceClusterRoleBinding := rbacv1.ClusterRoleBinding{
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					APIGroup:  "",
					Name:      sa.GetName(),
					Namespace: sa.GetNamespace(),
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     nonResourceClusterRole.GetName(),
			},
		}
		nonResourceClusterRoleBinding.SetName(genName("dep-"))
		_, err = c.CreateClusterRoleBinding(&nonResourceClusterRoleBinding)
		Expect(err).ShouldNot(HaveOccurred(), "could not create ClusterRoleBinding")

		ctx.Ctx().Logf("checking for deployment")
		// Poll for deployment to be ready
		Eventually(func() (bool, error) {
			dep, err := c.GetDeployment(testNamespace, depName)
			if k8serrors.IsNotFound(err) {
				ctx.Ctx().Logf("deployment %s not found\n", depName)
				return false, nil
			} else if err != nil {
				ctx.Ctx().Logf("unexpected error fetching deployment %s\n", depName)
				return false, err
			}
			if dep.Status.UpdatedReplicas == *(dep.Spec.Replicas) &&
				dep.Status.Replicas == *(dep.Spec.Replicas) &&
				dep.Status.AvailableReplicas == *(dep.Spec.Replicas) {
				ctx.Ctx().Logf("deployment ready")
				return true, nil
			}
			ctx.Ctx().Logf("deployment not ready")
			return false, nil
		}).Should(BeTrue())

		fetchedCSV, err = fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Delete CRD
		cleanupCRD()

		// Wait for CSV failure
		fetchedCSV, err = fetchCSV(crc, csv.Name, testNamespace, csvPendingChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Recreate the CRD
		cleanupCRD, err = createCRD(c, crd)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCRD()

		// Wait for CSV success again
		fetchedCSV, err = fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())
	})
	It("create requirements met API service", func() {

		sa := corev1.ServiceAccount{}
		sa.SetName(genName("sa-"))
		sa.SetNamespace(testNamespace)
		_, err := c.CreateServiceAccount(&sa)
		Expect(err).ShouldNot(HaveOccurred(), "could not create ServiceAccount")

		permissions := []v1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: sa.GetName(),
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"create"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
				},
			},
		}

		clusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: sa.GetName(),
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"get"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
				},
			},
		}

		depName := genName("dep-")
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: newNginxInstallStrategy(depName, permissions, clusterPermissions),
				// Cheating a little; this is an APIservice that will exist for the e2e tests
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Required: []v1alpha1.APIServiceDescription{
						{
							Group:       "packages.operators.coreos.com",
							Version:     "v1",
							Kind:        "PackageManifest",
							DisplayName: "Package Manifest",
							Description: "An apiservice that exists",
						},
					},
				},
			},
		}

		// Create Role/Cluster Roles and RoleBindings
		role := rbacv1.Role{
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"create"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		}
		role.SetName(genName("dep-"))
		role.SetNamespace(testNamespace)
		_, err = c.CreateRole(&role)
		Expect(err).ShouldNot(HaveOccurred(), "could not create Role")

		roleBinding := rbacv1.RoleBinding{
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					APIGroup:  "",
					Name:      sa.GetName(),
					Namespace: sa.GetNamespace(),
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     role.GetName(),
			},
		}
		roleBinding.SetName(genName("dep-"))
		roleBinding.SetNamespace(testNamespace)
		_, err = c.CreateRoleBinding(&roleBinding)
		Expect(err).ShouldNot(HaveOccurred(), "could not create RoleBinding")

		clusterRole := rbacv1.ClusterRole{
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		}
		clusterRole.SetName(genName("dep-"))
		_, err = c.CreateClusterRole(&clusterRole)
		Expect(err).ShouldNot(HaveOccurred(), "could not create ClusterRole")

		clusterRoleBinding := rbacv1.ClusterRoleBinding{
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					APIGroup:  "",
					Name:      sa.GetName(),
					Namespace: sa.GetNamespace(),
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     clusterRole.GetName(),
			},
		}
		clusterRoleBinding.SetName(genName("dep-"))
		_, err = c.CreateClusterRoleBinding(&clusterRoleBinding)
		Expect(err).ShouldNot(HaveOccurred(), "could not create ClusterRoleBinding")

		cleanupCSV, err := createCSV(c, crc, csv, testNamespace, false, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCSV()

		fetchedCSV, err := fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Fetch cluster service version again to check for unnecessary control loops
		sameCSV, err := fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())
		compareResources(GinkgoT(), fetchedCSV, sameCSV)
	})
	It("create with owned API service", func() {

		depName := genName("hat-server")
		mockGroup := fmt.Sprintf("hats.%s.redhat.com", genName(""))
		version := "v1alpha1"
		mockGroupVersion := strings.Join([]string{mockGroup, version}, "/")
		mockKinds := []string{"fez", "fedora"}
		depSpec := newMockExtServerDeployment(depName, []mockGroupVersionKind{{depName, mockGroupVersion, mockKinds, 5443}})
		apiServiceName := strings.Join([]string{version, mockGroup}, ".")

		// Create CSV for the package-server
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: depName,
					Spec: depSpec,
				},
			},
		}

		owned := make([]v1alpha1.APIServiceDescription, len(mockKinds))
		for i, kind := range mockKinds {
			owned[i] = v1alpha1.APIServiceDescription{
				Name:           apiServiceName,
				Group:          mockGroup,
				Version:        version,
				Kind:           kind,
				DeploymentName: depName,
				ContainerPort:  int32(5443),
				DisplayName:    kind,
				Description:    fmt.Sprintf("A %s", kind),
			}
		}

		csv := v1alpha1.ClusterServiceVersion{
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Owned: owned,
				},
			},
		}
		csv.SetName(depName)

		// Create the APIService CSV
		cleanupCSV, err := createCSV(c, crc, csv, testNamespace, false, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer func() {
			watcher, err := c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Watch(context.TODO(), metav1.ListOptions{FieldSelector: "metadata.name=" + apiServiceName})
			Expect(err).ShouldNot(HaveOccurred())

			deleted := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				events := watcher.ResultChan()
				for {
					select {
					case evt := <-events:
						if evt.Type == watch.Deleted {
							deleted <- struct{}{}
							return
						}
					case <-time.After(pollDuration):
						Fail("API service not cleaned up after CSV deleted")
					}
				}
			}()

			cleanupCSV()
			<-deleted
		}()

		fetchedCSV, err := fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Should create Deployment
		dep, err := c.GetDeployment(testNamespace, depName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected Deployment")

		// Should create APIService
		apiService, err := c.GetAPIService(apiServiceName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected APIService")

		// Should create Service
		serviceName := fmt.Sprintf("%s-service", depName)
		_, err = c.GetService(testNamespace, serviceName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected Service")

		// Should create certificate Secret
		secretName := fmt.Sprintf("%s-cert", serviceName)
		_, err = c.GetSecret(testNamespace, secretName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected Secret")

		// Should create a Role for the Secret
		_, err = c.GetRole(testNamespace, secretName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected Secret Role")

		// Should create a RoleBinding for the Secret
		_, err = c.GetRoleBinding(testNamespace, secretName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting exptected Secret RoleBinding")

		// Should create a system:auth-delegator Cluster RoleBinding
		_, err = c.GetClusterRoleBinding(fmt.Sprintf("%s-system:auth-delegator", serviceName))
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected system:auth-delegator ClusterRoleBinding")

		// Should create an extension-apiserver-authentication-reader RoleBinding in kube-system
		_, err = c.GetRoleBinding("kube-system", fmt.Sprintf("%s-auth-reader", serviceName))
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected extension-apiserver-authentication-reader RoleBinding")

		// Store the ca sha annotation
		oldCAAnnotation, ok := dep.Spec.Template.GetAnnotations()[install.OLMCAHashAnnotationKey]
		Expect(ok).Should(BeTrue(), "expected olm sha annotation not present on existing pod template")

		// Induce a cert rotation
		Eventually(Apply(fetchedCSV, func(csv *v1alpha1.ClusterServiceVersion) error {
			now := metav1.Now()
			csv.Status.CertsLastUpdated = &now
			csv.Status.CertsRotateAt = &now
			return nil
		})).Should(Succeed())

		_, err = fetchCSV(crc, csv.Name, testNamespace, func(csv *v1alpha1.ClusterServiceVersion) bool {
			// Should create deployment
			dep, err = c.GetDeployment(testNamespace, depName)
			if err != nil {
				return false
			}

			// Should have a new ca hash annotation
			newCAAnnotation, ok := dep.Spec.Template.GetAnnotations()[install.OLMCAHashAnnotationKey]
			if !ok {
				ctx.Ctx().Logf("expected olm sha annotation not present in new pod template")
				return false
			}

			if newCAAnnotation != oldCAAnnotation {
				// Check for success
				return csvSucceededChecker(csv)
			}

			return false
		})
		Expect(err).ShouldNot(HaveOccurred(), "failed to rotate cert")

		// Get the APIService UID
		oldAPIServiceUID := apiService.GetUID()

		// Delete the APIService
		err = c.DeleteAPIService(apiServiceName, &metav1.DeleteOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		// Wait for CSV success
		fetchedCSV, err = fetchCSV(crc, csv.GetName(), testNamespace, func(csv *v1alpha1.ClusterServiceVersion) bool {
			// Should create an APIService
			apiService, err := c.GetAPIService(apiServiceName)
			if err != nil {
				return false
			}

			if csvSucceededChecker(csv) {
				return apiService.GetUID() != oldAPIServiceUID
			}
			return false
		})
		Expect(err).ShouldNot(HaveOccurred())
	})
	It("update with owned API service", func() {

		depName := genName("hat-server")
		mockGroup := fmt.Sprintf("hats.%s.redhat.com", genName(""))
		version := "v1alpha1"
		mockGroupVersion := strings.Join([]string{mockGroup, version}, "/")
		mockKinds := []string{"fedora"}
		depSpec := newMockExtServerDeployment(depName, []mockGroupVersionKind{{depName, mockGroupVersion, mockKinds, 5443}})
		apiServiceName := strings.Join([]string{version, mockGroup}, ".")

		// Create CSVs for the hat-server
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: depName,
					Spec: depSpec,
				},
			},
		}

		owned := make([]v1alpha1.APIServiceDescription, len(mockKinds))
		for i, kind := range mockKinds {
			owned[i] = v1alpha1.APIServiceDescription{
				Name:           apiServiceName,
				Group:          mockGroup,
				Version:        version,
				Kind:           kind,
				DeploymentName: depName,
				ContainerPort:  int32(5443),
				DisplayName:    kind,
				Description:    fmt.Sprintf("A %s", kind),
			}
		}

		csv := v1alpha1.ClusterServiceVersion{
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Owned: owned,
				},
			},
		}
		csv.SetName("csv-hat-1")

		// Create the APIService CSV
		_, err := createCSV(c, crc, csv, testNamespace, false, false)
		Expect(err).ShouldNot(HaveOccurred())

		_, err = fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Should create Deployment
		_, err = c.GetDeployment(testNamespace, depName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected Deployment")

		// Should create APIService
		_, err = c.GetAPIService(apiServiceName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected APIService")

		// Should create Service
		serviceName := fmt.Sprintf("%s-service", depName)
		_, err = c.GetService(testNamespace, serviceName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected Service")

		// Should create certificate Secret
		secretName := fmt.Sprintf("%s-cert", serviceName)
		_, err = c.GetSecret(testNamespace, secretName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected Secret")

		// Should create a Role for the Secret
		_, err = c.GetRole(testNamespace, secretName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected Secret Role")

		// Should create a RoleBinding for the Secret
		_, err = c.GetRoleBinding(testNamespace, secretName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting exptected Secret RoleBinding")

		// Should create a system:auth-delegator Cluster RoleBinding
		_, err = c.GetClusterRoleBinding(fmt.Sprintf("%s-system:auth-delegator", serviceName))
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected system:auth-delegator ClusterRoleBinding")

		// Should create an extension-apiserver-authentication-reader RoleBinding in kube-system
		_, err = c.GetRoleBinding("kube-system", fmt.Sprintf("%s-auth-reader", serviceName))
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected extension-apiserver-authentication-reader RoleBinding")

		// Create a new CSV that owns the same API Service and replace the old CSV
		csv2 := v1alpha1.ClusterServiceVersion{
			Spec: v1alpha1.ClusterServiceVersionSpec{
				Replaces:       csv.Name,
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Owned: owned,
				},
			},
		}
		csv2.SetName("csv-hat-2")

		// Create CSV2 to replace CSV
		cleanupCSV2, err := createCSV(c, crc, csv2, testNamespace, false, true)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCSV2()

		_, err = fetchCSV(crc, csv2.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Should create Deployment
		_, err = c.GetDeployment(testNamespace, depName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected Deployment")

		// Should create APIService
		_, err = c.GetAPIService(apiServiceName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected APIService")

		// Should create Service
		Eventually(func() error {
			_, err := c.GetService(testNamespace, serviceName)
			return err
		}, timeout, interval).ShouldNot(HaveOccurred())

		// Should create certificate Secret
		secretName = fmt.Sprintf("%s-cert", serviceName)
		Eventually(func() error {
			_, err = c.GetSecret(testNamespace, secretName)
			return err
		}, timeout, interval).ShouldNot(HaveOccurred())

		// Should create a Role for the Secret
		_, err = c.GetRole(testNamespace, secretName)
		Eventually(func() error {
			_, err = c.GetRole(testNamespace, secretName)
			return err
		}, timeout, interval).ShouldNot(HaveOccurred())

		// Should create a RoleBinding for the Secret
		Eventually(func() error {
			_, err = c.GetRoleBinding(testNamespace, secretName)
			return err
		}, timeout, interval).ShouldNot(HaveOccurred())

		// Should create a system:auth-delegator Cluster RoleBinding
		Eventually(func() error {
			_, err = c.GetClusterRoleBinding(fmt.Sprintf("%s-system:auth-delegator", serviceName))
			return err
		}, timeout, interval).ShouldNot(HaveOccurred())

		// Should create an extension-apiserver-authentication-reader RoleBinding in kube-system
		Eventually(func() error {
			_, err = c.GetRoleBinding("kube-system", fmt.Sprintf("%s-auth-reader", serviceName))
			return err
		}, timeout, interval).ShouldNot(HaveOccurred())
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected extension-apiserver-authentication-reader RoleBinding")

		// Should eventually GC the CSV
		Eventually(func() bool {
			return csvExists(crc, csv.Name)
		}).Should(BeFalse())

		// Rename the initial CSV
		csv.SetName("csv-hat-3")

		// Recreate the old CSV
		cleanupCSV, err := createCSV(c, crc, csv, testNamespace, false, true)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCSV()

		fetched, err := fetchCSV(crc, csv.Name, testNamespace, buildCSVReasonChecker(v1alpha1.CSVReasonOwnerConflict))
		Expect(err).ShouldNot(HaveOccurred())
		Expect(fetched.Status.Phase).Should(Equal(v1alpha1.CSVPhaseFailed))
	})
	It("create same CSV with owned API service multi namespace", func() {

		// Create new namespace in a new operator group
		secondNamespaceName := genName(testNamespace + "-")
		matchingLabel := map[string]string{"inGroup": secondNamespaceName}

		_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   secondNamespaceName,
				Labels: matchingLabel,
			},
		}, metav1.CreateOptions{})
		Expect(err).ShouldNot(HaveOccurred())
		defer func() {
			err = c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), secondNamespaceName, metav1.DeleteOptions{})
			Expect(err).ShouldNot(HaveOccurred())
		}()

		// Create a new operator group for the new namespace
		operatorGroup := v1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      genName("e2e-operator-group-"),
				Namespace: secondNamespaceName,
			},
			Spec: v1.OperatorGroupSpec{
				Selector: &metav1.LabelSelector{
					MatchLabels: matchingLabel,
				},
			},
		}
		_, err = crc.OperatorsV1().OperatorGroups(secondNamespaceName).Create(context.TODO(), &operatorGroup, metav1.CreateOptions{})
		Expect(err).ShouldNot(HaveOccurred())
		defer func() {
			err = crc.OperatorsV1().OperatorGroups(secondNamespaceName).Delete(context.TODO(), operatorGroup.Name, metav1.DeleteOptions{})
			Expect(err).ShouldNot(HaveOccurred())
		}()

		ctx.Ctx().Logf("Waiting on new operator group to have correct status")
		Eventually(func() ([]string, error) {
			og, err := crc.OperatorsV1().OperatorGroups(secondNamespaceName).Get(context.TODO(), operatorGroup.Name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			return og.Status.Namespaces, nil
		}).Should(ConsistOf([]string{secondNamespaceName}))

		depName := genName("hat-server")
		mockGroup := fmt.Sprintf("hats.%s.redhat.com", genName(""))
		version := "v1alpha1"
		mockGroupVersion := strings.Join([]string{mockGroup, version}, "/")
		mockKinds := []string{"fedora"}
		depSpec := newMockExtServerDeployment(depName, []mockGroupVersionKind{{depName, mockGroupVersion, mockKinds, 5443}})
		apiServiceName := strings.Join([]string{version, mockGroup}, ".")

		// Create CSVs for the hat-server
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: depName,
					Spec: depSpec,
				},
			},
		}

		owned := make([]v1alpha1.APIServiceDescription, len(mockKinds))
		for i, kind := range mockKinds {
			owned[i] = v1alpha1.APIServiceDescription{
				Name:           apiServiceName,
				Group:          mockGroup,
				Version:        version,
				Kind:           kind,
				DeploymentName: depName,
				ContainerPort:  int32(5443),
				DisplayName:    kind,
				Description:    fmt.Sprintf("A %s", kind),
			}
		}

		csv := v1alpha1.ClusterServiceVersion{
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Owned: owned,
				},
			},
		}
		csv.SetName("csv-hat-1")

		// Create the initial CSV
		cleanupCSV, err := createCSV(c, crc, csv, testNamespace, false, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCSV()

		_, err = fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Should create Deployment
		_, err = c.GetDeployment(testNamespace, depName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected Deployment")

		// Should create APIService
		_, err = c.GetAPIService(apiServiceName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected APIService")

		// Should create Service
		serviceName := fmt.Sprintf("%s-service", depName)
		_, err = c.GetService(testNamespace, serviceName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected Service")

		// Should create certificate Secret
		secretName := fmt.Sprintf("%s-cert", serviceName)
		_, err = c.GetSecret(testNamespace, secretName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected Secret")

		// Should create a Role for the Secret
		_, err = c.GetRole(testNamespace, secretName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected Secret Role")

		// Should create a RoleBinding for the Secret
		_, err = c.GetRoleBinding(testNamespace, secretName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting exptected Secret RoleBinding")

		// Should create a system:auth-delegator Cluster RoleBinding
		_, err = c.GetClusterRoleBinding(fmt.Sprintf("%s-system:auth-delegator", serviceName))
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected system:auth-delegator ClusterRoleBinding")

		// Should create an extension-apiserver-authentication-reader RoleBinding in kube-system
		_, err = c.GetRoleBinding("kube-system", fmt.Sprintf("%s-auth-reader", serviceName))
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected extension-apiserver-authentication-reader RoleBinding")

		// Create a new CSV that owns the same API Service but in a different namespace
		csv2 := v1alpha1.ClusterServiceVersion{
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Owned: owned,
				},
			},
		}
		csv2.SetName("csv-hat-2")

		// Create CSV2 to replace CSV
		_, err = createCSV(c, crc, csv2, secondNamespaceName, false, true)
		Expect(err).ShouldNot(HaveOccurred())

		_, err = fetchCSV(crc, csv2.Name, secondNamespaceName, csvFailedChecker)
		Expect(err).ShouldNot(HaveOccurred())
	})
	It("orphaned API service clean up", func() {

		mockGroup := fmt.Sprintf("hats.%s.redhat.com", genName(""))
		version := "v1alpha1"
		apiServiceName := strings.Join([]string{version, mockGroup}, ".")

		apiService := &apiregistrationv1.APIService{
			ObjectMeta: metav1.ObjectMeta{
				Name: apiServiceName,
			},
			Spec: apiregistrationv1.APIServiceSpec{
				Group:                mockGroup,
				Version:              version,
				GroupPriorityMinimum: 100,
				VersionPriority:      100,
			},
		}

		watcher, err := c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Watch(context.TODO(), metav1.ListOptions{FieldSelector: "metadata.name=" + apiServiceName})
		Expect(err).ShouldNot(HaveOccurred())

		deleted := make(chan struct{})
		quit := make(chan struct{})
		defer close(quit)
		go func() {
			defer GinkgoRecover()
			events := watcher.ResultChan()
			for {
				select {
				case <-quit:
					return
				case evt := <-events:
					if evt.Type == watch.Deleted {
						deleted <- struct{}{}
					}
				case <-time.After(pollDuration):
					Fail("orphaned apiservice not cleaned up as expected")
				}
			}
		}()

		_, err = c.CreateAPIService(apiService)
		Expect(err).ShouldNot(HaveOccurred(), "error creating expected APIService")
		orphanedAPISvc, err := c.GetAPIService(apiServiceName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected APIService")

		newLabels := map[string]string{"olm.owner": "hat-serverfd4r5", "olm.owner.kind": "ClusterServiceVersion", "olm.owner.namespace": "nonexistent-namespace"}
		orphanedAPISvc.SetLabels(newLabels)
		_, err = c.UpdateAPIService(orphanedAPISvc)
		Expect(err).ShouldNot(HaveOccurred(), "error updating APIService")
		<-deleted

		_, err = c.CreateAPIService(apiService)
		Expect(err).ShouldNot(HaveOccurred(), "error creating expected APIService")
		orphanedAPISvc, err = c.GetAPIService(apiServiceName)
		Expect(err).ShouldNot(HaveOccurred(), "error getting expected APIService")

		newLabels = map[string]string{"olm.owner": "hat-serverfd4r5", "olm.owner.kind": "ClusterServiceVersion", "olm.owner.namespace": testNamespace}
		orphanedAPISvc.SetLabels(newLabels)
		_, err = c.UpdateAPIService(orphanedAPISvc)
		Expect(err).ShouldNot(HaveOccurred(), "error updating APIService")
		<-deleted
	})
	It("CSV annotations overwrite pod template annotations defined in a StrategyDetailsDeployment", func() {
		// Create a StrategyDetailsDeployment that defines the `foo1` and `foo2` annotations on a pod template
		nginxName := genName("nginx-")
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep-"),
					Spec: newNginxDeployment(nginxName),
				},
			},
		}
		strategy.DeploymentSpecs[0].Spec.Template.Annotations = map[string]string{
			"foo1": "notBar1",
			"foo2": "bar2",
		}

		// Create a CSV that defines the `foo1` and `foo3` annotations
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
				Annotations: map[string]string{
					"foo1": "bar1",
					"foo3": "bar3",
				},
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
			},
		}

		// Create the CSV and make sure to clean it up
		cleanupCSV, err := createCSV(c, crc, csv, testNamespace, false, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCSV()

		// Wait for current CSV to succeed
		_, err = fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Should have created deployment
		dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(dep).ShouldNot(BeNil())

		// Make sure that the pods annotations are correct
		annotations := dep.Spec.Template.Annotations
		Expect(annotations["foo1"]).Should(Equal("bar1"))
		Expect(annotations["foo2"]).Should(Equal("bar2"))
		Expect(annotations["foo3"]).Should(Equal("bar3"))
	})
	It("update same deployment name", func() {

		// Create dependency first (CRD)
		crdPlural := genName("ins")
		crdName := crdPlural + ".cluster.com"
		cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group:   "cluster.com",
				Version: "v1alpha1",
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		})

		// Create "current" CSV
		nginxName := genName("nginx-")
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep-"),
					Spec: newNginxDeployment(nginxName),
				},
			},
		}

		Expect(err).ShouldNot(HaveOccurred())

		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCRD()
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster",
						},
					},
				},
			},
		}

		// Don't need to cleanup this CSV, it will be deleted by the upgrade process
		_, err = createCSV(c, crc, csv, testNamespace, false, false)
		Expect(err).ShouldNot(HaveOccurred())

		// Wait for current CSV to succeed
		_, err = fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Should have created deployment
		dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(dep).ShouldNot(BeNil())

		// Create "updated" CSV
		strategyNew := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					// Same name
					Name: strategy.DeploymentSpecs[0].Name,
					// Different spec
					Spec: newNginxDeployment(nginxName),
				},
			},
		}

		Expect(err).ShouldNot(HaveOccurred())

		csvNew := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				Replaces: csv.Name,
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategyNew,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster",
						},
					},
				},
			},
		}

		cleanupNewCSV, err := createCSV(c, crc, csvNew, testNamespace, true, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupNewCSV()

		// Wait for updated CSV to succeed
		fetchedCSV, err := fetchCSV(crc, csvNew.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Should have updated existing deployment
		depUpdated, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(depUpdated).ShouldNot(BeNil())
		Expect(strategyNew.DeploymentSpecs[0].Spec.Template.Spec.Containers[0].Name).Should(Equal(depUpdated.Spec.Template.Spec.Containers[0].Name))

		// Should eventually GC the CSV
		Eventually(func() bool {
			return csvExists(crc, csv.Name)
		}).Should(BeFalse())

		// Fetch cluster service version again to check for unnecessary control loops
		sameCSV, err := fetchCSV(crc, csvNew.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())
		compareResources(GinkgoT(), fetchedCSV, sameCSV)
	})
	It("update different deployment name", func() {

		// Create dependency first (CRD)
		crdPlural := genName("ins2")
		crdName := crdPlural + ".cluster.com"
		cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group:   "cluster.com",
				Version: "v1alpha1",
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		})
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCRD()

		// create "current" CSV
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep-"),
					Spec: newNginxDeployment(genName("nginx-")),
				},
			},
		}

		Expect(err).ShouldNot(HaveOccurred())

		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster2",
						},
					},
				},
			},
		}

		// don't need to clean up this CSV, it will be deleted by the upgrade process
		_, err = createCSV(c, crc, csv, testNamespace, false, false)
		Expect(err).ShouldNot(HaveOccurred())

		// Wait for current CSV to succeed
		_, err = fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Should have created deployment
		dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(dep).ShouldNot(BeNil())

		// Create "updated" CSV
		strategyNew := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep2"),
					Spec: newNginxDeployment(genName("nginx-")),
				},
			},
		}

		Expect(err).ShouldNot(HaveOccurred())

		csvNew := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv2"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				Replaces: csv.Name,
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategyNew,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster2",
						},
					},
				},
			},
		}

		cleanupNewCSV, err := createCSV(c, crc, csvNew, testNamespace, true, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupNewCSV()

		// Wait for updated CSV to succeed
		fetchedCSV, err := fetchCSV(crc, csvNew.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Fetch cluster service version again to check for unnecessary control loops
		sameCSV, err := fetchCSV(crc, csvNew.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())
		compareResources(GinkgoT(), fetchedCSV, sameCSV)

		// Should have created new deployment and deleted old
		depNew, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(depNew).ShouldNot(BeNil())
		err = waitForDeploymentToDelete(c, strategy.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())

		// Should eventually GC the CSV
		Eventually(func() bool {
			return csvExists(crc, csv.Name)
		}).Should(BeFalse())
	})
	It("update multiple intermediates", func() {

		// Create dependency first (CRD)
		crdPlural := genName("ins3")
		crdName := crdPlural + ".cluster.com"
		cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group: "cluster.com",
				Versions: []apiextensions.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha1",
						Served:  true,
						Storage: true,
					},
				},
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		})
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCRD()

		// create "current" CSV
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep-"),
					Spec: newNginxDeployment(genName("nginx-")),
				},
			},
		}

		Expect(err).ShouldNot(HaveOccurred())

		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster3",
						},
					},
				},
			},
		}

		// don't need to clean up this CSV, it will be deleted by the upgrade process
		_, err = createCSV(c, crc, csv, testNamespace, false, false)
		Expect(err).ShouldNot(HaveOccurred())

		// Wait for current CSV to succeed
		_, err = fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Should have created deployment
		dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(dep).ShouldNot(BeNil())

		// Create "updated" CSV
		strategyNew := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep2"),
					Spec: newNginxDeployment(genName("nginx-")),
				},
			},
		}

		Expect(err).ShouldNot(HaveOccurred())

		csvNew := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv2"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				Replaces: csv.Name,
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategyNew,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster3",
						},
					},
				},
			},
		}

		cleanupNewCSV, err := createCSV(c, crc, csvNew, testNamespace, true, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupNewCSV()

		// Wait for updated CSV to succeed
		fetchedCSV, err := fetchCSV(crc, csvNew.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Fetch cluster service version again to check for unnecessary control loops
		sameCSV, err := fetchCSV(crc, csvNew.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())
		compareResources(GinkgoT(), fetchedCSV, sameCSV)

		// Should have created new deployment and deleted old
		depNew, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(depNew).ShouldNot(BeNil())
		err = waitForDeploymentToDelete(c, strategy.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())

		// Should eventually GC the CSV
		Eventually(func() bool {
			return csvExists(crc, csv.Name)
		}).Should(BeFalse())
	})
	It("update in place", func() {

		// Create dependency first (CRD)
		crdPlural := genName("ins")
		crdName := crdPlural + ".cluster.com"
		cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group:   "cluster.com",
				Version: "v1alpha1",
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		})

		// Create "current" CSV
		nginxName := genName("nginx-")
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep-"),
					Spec: newNginxDeployment(nginxName),
				},
			},
		}

		Expect(err).ShouldNot(HaveOccurred())

		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCRD()
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster",
						},
					},
				},
			},
		}

		cleanupCSV, err := createCSV(c, crc, csv, testNamespace, false, true)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCSV()

		// Wait for current CSV to succeed
		fetchedCSV, err := fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Should have created deployment
		dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(dep).ShouldNot(BeNil())

		// Create "updated" CSV
		strategyNew := strategy
		strategyNew.DeploymentSpecs[0].Spec.Template.Spec.Containers = []corev1.Container{
			{
				Name:  genName("nginx-"),
				Image: *dummyImage,
				Ports: []corev1.ContainerPort{
					{
						ContainerPort: 80,
					},
				},
				ImagePullPolicy: corev1.PullIfNotPresent,
			},
		}

		// Also set something outside the spec template - this should be ignored
		var five int32 = 5
		strategyNew.DeploymentSpecs[0].Spec.Replicas = &five

		Expect(err).ShouldNot(HaveOccurred())

		fetchedCSV.Spec.InstallStrategy.StrategySpec = strategyNew

		// Update CSV directly
		_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Update(context.TODO(), fetchedCSV, metav1.UpdateOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		// wait for deployment spec to be updated
		Eventually(func() (string, error) {
			fetched, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
			if err != nil {
				return "", err
			}
			ctx.Ctx().Logf("waiting for deployment to update...")
			return fetched.Spec.Template.Spec.Containers[0].Name, nil
		}).Should(Equal(strategyNew.DeploymentSpecs[0].Spec.Template.Spec.Containers[0].Name))

		// Wait for updated CSV to succeed
		_, err = fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		depUpdated, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(depUpdated).ShouldNot(BeNil())

		// Deployment should have changed even though the CSV is otherwise the same
		Expect(strategyNew.DeploymentSpecs[0].Spec.Template.Spec.Containers[0].Name).Should(Equal(depUpdated.Spec.Template.Spec.Containers[0].Name))
		Expect(strategyNew.DeploymentSpecs[0].Spec.Template.Spec.Containers[0].Image).Should(Equal(depUpdated.Spec.Template.Spec.Containers[0].Image))

		// Field updated even though template spec didn't change, because it was part of a template spec change as well
		Expect(*strategyNew.DeploymentSpecs[0].Spec.Replicas).Should(Equal(*depUpdated.Spec.Replicas))
	})
	It("update multiple version CRD", func() {

		// Create initial CRD which has 2 versions: v1alpha1 & v1alpha2
		crdPlural := genName("ins4")
		crdName := crdPlural + ".cluster.com"
		cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group: "cluster.com",
				Versions: []apiextensions.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha1",
						Served:  true,
						Storage: true,
					},
					{
						Name:    "v1alpha2",
						Served:  true,
						Storage: false,
					},
				},
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		})
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCRD()

		// create initial deployment strategy
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep1-"),
					Spec: newNginxDeployment(genName("nginx-")),
				},
			},
		}

		Expect(err).ShouldNot(HaveOccurred())

		// First CSV with owning CRD v1alpha1
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster4",
						},
					},
				},
			},
		}

		// CSV will be deleted by the upgrade process later
		_, err = createCSV(c, crc, csv, testNamespace, false, false)
		Expect(err).ShouldNot(HaveOccurred())

		// Wait for current CSV to succeed
		_, err = fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Should have created deployment
		dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(dep).ShouldNot(BeNil())

		// Create updated deployment strategy
		strategyNew := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep2-"),
					Spec: newNginxDeployment(genName("nginx-")),
				},
			},
		}

		Expect(err).ShouldNot(HaveOccurred())

		// Second CSV with owning CRD v1alpha1 and v1alpha2
		csvNew := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv2"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				Replaces: csv.Name,
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategyNew,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster4",
						},
						{
							Name:        crdName,
							Version:     "v1alpha2",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster4",
						},
					},
				},
			},
		}

		// Create newly updated CSV
		_, err = createCSV(c, crc, csvNew, testNamespace, false, false)
		Expect(err).ShouldNot(HaveOccurred())

		// Wait for updated CSV to succeed
		fetchedCSV, err := fetchCSV(crc, csvNew.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Fetch cluster service version again to check for unnecessary control loops
		sameCSV, err := fetchCSV(crc, csvNew.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())
		compareResources(GinkgoT(), fetchedCSV, sameCSV)

		// Should have created new deployment and deleted old one
		depNew, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(depNew).ShouldNot(BeNil())
		err = waitForDeploymentToDelete(c, strategy.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())

		// Create updated deployment strategy
		strategyNew2 := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep3-"),
					Spec: newNginxDeployment(genName("nginx-")),
				},
			},
		}
		Expect(err).ShouldNot(HaveOccurred())

		// Third CSV with owning CRD v1alpha2
		csvNew2 := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv3"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				Replaces: csvNew.Name,
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategyNew2,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha2",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster4",
						},
					},
				},
			},
		}

		// Create newly updated CSV
		cleanupNewCSV, err := createCSV(c, crc, csvNew2, testNamespace, true, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupNewCSV()

		// Wait for updated CSV to succeed
		fetchedCSV, err = fetchCSV(crc, csvNew2.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Fetch cluster service version again to check for unnecessary control loops
		sameCSV, err = fetchCSV(crc, csvNew2.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())
		compareResources(GinkgoT(), fetchedCSV, sameCSV)

		// Should have created new deployment and deleted old one
		depNew, err = c.GetDeployment(testNamespace, strategyNew2.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(depNew).ShouldNot(BeNil())
		err = waitForDeploymentToDelete(c, strategyNew.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())

		// Should clean up the CSV
		Eventually(func() bool {
			return csvExists(crc, csvNew.Name)
		}).Should(BeFalse())
	})
	It("update modify deployment name", func() {

		// Create dependency first (CRD)
		crdPlural := genName("ins2")
		crdName := crdPlural + ".cluster.com"
		cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group:   "cluster.com",
				Version: "v1alpha1",
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		})
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCRD()

		// create "current" CSV
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep-"),
					Spec: newNginxDeployment(genName("nginx-")),
				},
				{
					Name: "dep2-test",
					Spec: newNginxDeployment("nginx2"),
				},
			},
		}

		Expect(err).ShouldNot(HaveOccurred())

		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster2",
						},
					},
				},
			},
		}

		cleanupCSV, err := createCSV(c, crc, csv, testNamespace, true, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCSV()

		// Wait for current CSV to succeed
		_, err = fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Should have created deployments
		dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(dep).ShouldNot(BeNil())
		dep2, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[1].Name)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(dep2).ShouldNot(BeNil())

		// Create "updated" CSV
		strategyNew := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep3-"),
					Spec: newNginxDeployment(genName("nginx3-")),
				},
				{
					Name: "dep2-test",
					Spec: newNginxDeployment("nginx2"),
				},
			},
		}

		Expect(err).ShouldNot(HaveOccurred())

		// Fetch the current csv
		fetchedCSV, err := fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Update csv with same strategy with different deployment's name
		fetchedCSV.Spec.InstallStrategy.StrategySpec = strategyNew

		// Update the current csv with the new csv
		_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Update(context.TODO(), fetchedCSV, metav1.UpdateOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		// Wait for new deployment to exist
		err = waitForDeployment(c, strategyNew.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())

		// Wait for updated CSV to succeed
		_, err = fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Should have created new deployment and deleted old
		depNew, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(depNew).ShouldNot(BeNil())

		// Make sure the unchanged deployment still exists
		depNew2, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[1].Name)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(depNew2).ShouldNot(BeNil())

		err = waitForDeploymentToDelete(c, strategy.DeploymentSpecs[0].Name)
		Expect(err).ShouldNot(HaveOccurred())
	})

	It("emits CSV requirement events", func() {

		csv := &v1alpha1.ClusterServiceVersion{
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: newNginxInstallStrategy(genName("dep-"), nil, nil),
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					// Require an API that we know won't exist under our domain
					Required: []v1alpha1.APIServiceDescription{
						{
							Group:   "bad.packages.operators.coreos.com",
							Version: "v1",
							Kind:    "PackageManifest",
						},
					},
				},
			},
		}
		csv.SetNamespace(testNamespace)
		csv.SetName(genName("csv-"))

		clientCtx := context.Background()
		listOpts := metav1.ListOptions{
			FieldSelector: "involvedObject.kind=ClusterServiceVersion",
		}
		events, err := c.KubernetesInterface().CoreV1().Events(csv.GetNamespace()).List(clientCtx, listOpts)
		Expect(err).ToNot(HaveOccurred())

		// Watch latest events from test namespace for CSV
		listOpts.ResourceVersion = events.ResourceVersion
		w, err := c.KubernetesInterface().CoreV1().Events(testNamespace).Watch(context.Background(), listOpts)
		Expect(err).ToNot(HaveOccurred())
		defer w.Stop()

		cleanupCSV, err := createCSV(c, crc, *csv, csv.GetNamespace(), false, false)
		Expect(err).ToNot(HaveOccurred())
		defer cleanupCSV()

		csv, err = fetchCSV(crc, csv.GetName(), csv.GetNamespace(), csvPendingChecker)
		Expect(err).ToNot(HaveOccurred())

		By("emitting when requirements are not met")
		nextReason := func() string {
			e := <-w.ResultChan()
			if e.Object == nil {
				return ""
			}

			return e.Object.(*corev1.Event).Reason
		}
		Eventually(nextReason).Should(Equal("RequirementsNotMet"))

		// Update the CSV to require an API that we know exists
		csv.Spec.APIServiceDefinitions.Required[0].Group = "packages.operators.coreos.com"
		updateOpts := metav1.UpdateOptions{}
		Eventually(func() error {
			_, err := crc.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Update(clientCtx, csv, updateOpts)
			return err
		}).Should(Succeed())

		By("emitting when requirements are met")
		Eventually(nextReason).Should(Equal("AllRequirementsMet"))
	})

	// TODO: test behavior when replaces field doesn't point to existing CSV
	It("status invalid CSV", func() {

		// Create CRD
		crdPlural := genName("ins")
		crdName := crdPlural + ".cluster.com"
		cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group:   "cluster.com",
				Version: "v1alpha1",
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		})
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCRD()

		// create CSV
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep-"),
					Spec: newNginxDeployment(genName("nginx-")),
				},
			},
		}

		Expect(err).ShouldNot(HaveOccurred())

		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "apiextensions.k8s.io/v1alpha1", // purposely invalid, should be just v1alpha1 to match CRD
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster2",
						},
					},
				},
			},
		}

		cleanupCSV, err := createCSV(c, crc, csv, testNamespace, true, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCSV()

		notServedStatus := v1alpha1.RequirementStatus{
			Group:   "apiextensions.k8s.io",
			Version: "v1",
			Kind:    "CustomResourceDefinition",
			Name:    crdName,
			Status:  v1alpha1.RequirementStatusReasonNotPresent,
			Message: "CRD version not served",
		}
		csvCheckPhaseAndRequirementStatus := func(csv *v1alpha1.ClusterServiceVersion) bool {
			if csv.Status.Phase == v1alpha1.CSVPhasePending {
				for _, status := range csv.Status.RequirementStatus {
					if status.Message == notServedStatus.Message {
						return true
					}
				}
			}
			return false
		}

		fetchedCSV, err := fetchCSV(crc, csv.Name, testNamespace, csvCheckPhaseAndRequirementStatus)
		Expect(err).ShouldNot(HaveOccurred())

		Expect(fetchedCSV.Status.RequirementStatus).Should(ContainElement(notServedStatus))
	})

	It("api service resource migrated if adoptable", func() {

		depName := genName("hat-server")
		mockGroup := fmt.Sprintf("hats.%s.redhat.com", genName(""))
		version := "v1alpha1"
		mockGroupVersion := strings.Join([]string{mockGroup, version}, "/")
		mockKinds := []string{"fedora"}
		depSpec := newMockExtServerDeployment(depName, []mockGroupVersionKind{{depName, mockGroupVersion, mockKinds, 5443}})
		apiServiceName := strings.Join([]string{version, mockGroup}, ".")

		// Create CSVs for the hat-server
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: depName,
					Spec: depSpec,
				},
			},
		}

		owned := make([]v1alpha1.APIServiceDescription, len(mockKinds))
		for i, kind := range mockKinds {
			owned[i] = v1alpha1.APIServiceDescription{
				Name:           apiServiceName,
				Group:          mockGroup,
				Version:        version,
				Kind:           kind,
				DeploymentName: depName,
				ContainerPort:  int32(5443),
				DisplayName:    kind,
				Description:    fmt.Sprintf("A %s", kind),
			}
		}

		csv := v1alpha1.ClusterServiceVersion{
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Owned: owned,
				},
			},
		}
		csv.SetName("csv-hat-1")
		csv.SetNamespace(testNamespace)

		createLegacyAPIResources(&csv, owned[0])

		// Create the APIService CSV
		cleanupCSV, err := createCSV(c, crc, csv, testNamespace, false, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCSV()

		_, err = fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		checkLegacyAPIResources(owned[0], true)
	})

	It("API service resource not migrated if not adoptable", func() {

		depName := genName("hat-server")
		mockGroup := fmt.Sprintf("hats.%s.redhat.com", genName(""))
		version := "v1alpha1"
		mockGroupVersion := strings.Join([]string{mockGroup, version}, "/")
		mockKinds := []string{"fedora"}
		depSpec := newMockExtServerDeployment(depName, []mockGroupVersionKind{{depName, mockGroupVersion, mockKinds, 5443}})
		apiServiceName := strings.Join([]string{version, mockGroup}, ".")

		// Create CSVs for the hat-server
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: depName,
					Spec: depSpec,
				},
			},
		}

		owned := make([]v1alpha1.APIServiceDescription, len(mockKinds))
		for i, kind := range mockKinds {
			owned[i] = v1alpha1.APIServiceDescription{
				Name:           apiServiceName,
				Group:          mockGroup,
				Version:        version,
				Kind:           kind,
				DeploymentName: depName,
				ContainerPort:  int32(5443),
				DisplayName:    kind,
				Description:    fmt.Sprintf("A %s", kind),
			}
		}

		csv := v1alpha1.ClusterServiceVersion{
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Owned: owned,
				},
			},
		}
		csv.SetName("csv-hat-1")
		csv.SetNamespace(testNamespace)

		createLegacyAPIResources(nil, owned[0])

		// Create the APIService CSV
		cleanupCSV, err := createCSV(c, crc, csv, testNamespace, false, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCSV()

		_, err = fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		checkLegacyAPIResources(owned[0], false)

		// Cleanup the resources created for this test that were not cleaned up.
		deleteLegacyAPIResources(owned[0])
	})

	It("multiple API services on a single pod", func() {

		// Create the deployment that both APIServices will be deployed to.
		depName := genName("hat-server")

		// Define the expected mock APIService settings.
		version := "v1alpha1"
		apiService1Group := fmt.Sprintf("hats.%s.redhat.com", genName(""))
		apiService1GroupVersion := strings.Join([]string{apiService1Group, version}, "/")
		apiService1Kinds := []string{"fedora"}
		apiService1Name := strings.Join([]string{version, apiService1Group}, ".")

		apiService2Group := fmt.Sprintf("hats.%s.redhat.com", genName(""))
		apiService2GroupVersion := strings.Join([]string{apiService2Group, version}, "/")
		apiService2Kinds := []string{"fez"}
		apiService2Name := strings.Join([]string{version, apiService2Group}, ".")

		// Create the deployment spec with the two APIServices.
		mockGroupVersionKinds := []mockGroupVersionKind{
			{
				depName,
				apiService1GroupVersion,
				apiService1Kinds,
				5443,
			},
			{
				depName,
				apiService2GroupVersion,
				apiService2Kinds,
				5444,
			},
		}
		depSpec := newMockExtServerDeployment(depName, mockGroupVersionKinds)

		// Create the CSV.
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: depName,
					Spec: depSpec,
				},
			},
		}

		// Update the owned APIServices two include the two APIServices.
		owned := []v1alpha1.APIServiceDescription{
			{
				Name:           apiService1Name,
				Group:          apiService1Group,
				Version:        version,
				Kind:           apiService1Kinds[0],
				DeploymentName: depName,
				ContainerPort:  int32(5443),
				DisplayName:    apiService1Kinds[0],
				Description:    fmt.Sprintf("A %s", apiService1Kinds[0]),
			},
			{
				Name:           apiService2Name,
				Group:          apiService2Group,
				Version:        version,
				Kind:           apiService2Kinds[0],
				DeploymentName: depName,
				ContainerPort:  int32(5444),
				DisplayName:    apiService2Kinds[0],
				Description:    fmt.Sprintf("A %s", apiService2Kinds[0]),
			},
		}

		csv := v1alpha1.ClusterServiceVersion{
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Owned: owned,
				},
			},
		}
		csv.SetName("csv-hat-1")
		csv.SetNamespace(testNamespace)

		// Create the APIService CSV
		cleanupCSV, err := createCSV(c, crc, csv, testNamespace, false, false)
		Expect(err).ShouldNot(HaveOccurred())
		defer cleanupCSV()

		_, err = fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Check that the APIService caBundles are equal
		apiService1, err := c.GetAPIService(apiService1Name)
		Expect(err).ShouldNot(HaveOccurred())

		apiService2, err := c.GetAPIService(apiService2Name)
		Expect(err).ShouldNot(HaveOccurred())

		Expect(apiService2.Spec.CABundle).Should(Equal(apiService1.Spec.CABundle))
	})
})

var singleInstance = int32(1)

type cleanupFunc func()

var immediateDeleteGracePeriod int64 = 0

func findLastEvent(events *corev1.EventList) (event corev1.Event) {
	var latestTime metav1.Time
	var latestInd int
	for i, item := range events.Items {
		if i != 0 {
			if latestTime.Before(&item.LastTimestamp) {
				latestTime = item.LastTimestamp
				latestInd = i
			}
		} else {
			latestTime = item.LastTimestamp
		}
	}
	return events.Items[latestInd]
}

func buildCSVCleanupFunc(c operatorclient.ClientInterface, crc versioned.Interface, csv v1alpha1.ClusterServiceVersion, namespace string, deleteCRDs, deleteAPIServices bool) cleanupFunc {
	return func() {
		err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Delete(context.TODO(), csv.GetName(), metav1.DeleteOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		if deleteCRDs {
			for _, crd := range csv.Spec.CustomResourceDefinitions.Owned {
				buildCRDCleanupFunc(c, crd.Name)()
			}
		}

		if deleteAPIServices {
			for _, desc := range csv.GetOwnedAPIServiceDescriptions() {
				buildAPIServiceCleanupFunc(c, desc.Name)()
			}
		}

		err = waitForDelete(func() error {
			_, err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(context.TODO(), csv.GetName(), metav1.GetOptions{})
			return err
		})
		Expect(err).ShouldNot(HaveOccurred())

	}
}

func createCSV(c operatorclient.ClientInterface, crc versioned.Interface, csv v1alpha1.ClusterServiceVersion, namespace string, cleanupCRDs, cleanupAPIServices bool) (cleanupFunc, error) {
	csv.Kind = v1alpha1.ClusterServiceVersionKind
	csv.APIVersion = v1alpha1.SchemeGroupVersion.String()
	Eventually(func() error {
		_, err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Create(context.TODO(), &csv, metav1.CreateOptions{})
		return err
	}).Should(Succeed())

	return buildCSVCleanupFunc(c, crc, csv, namespace, cleanupCRDs, cleanupAPIServices), nil

}

func buildCRDCleanupFunc(c operatorclient.ClientInterface, crdName string) cleanupFunc {
	return func() {
		err := c.ApiextensionsInterface().ApiextensionsV1beta1().CustomResourceDefinitions().Delete(context.TODO(), crdName, *metav1.NewDeleteOptions(immediateDeleteGracePeriod))
		if err != nil {
			fmt.Println(err)
		}

		waitForDelete(func() error {
			_, err := c.ApiextensionsInterface().ApiextensionsV1beta1().CustomResourceDefinitions().Get(context.TODO(), crdName, metav1.GetOptions{})
			return err
		})
	}
}

func buildAPIServiceCleanupFunc(c operatorclient.ClientInterface, apiServiceName string) cleanupFunc {
	return func() {
		err := c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Delete(context.TODO(), apiServiceName, *metav1.NewDeleteOptions(immediateDeleteGracePeriod))
		if err != nil {
			fmt.Println(err)
		}

		waitForDelete(func() error {
			_, err := c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Get(context.TODO(), apiServiceName, metav1.GetOptions{})
			return err
		})
	}
}

func createCRD(c operatorclient.ClientInterface, crd apiextensions.CustomResourceDefinition) (cleanupFunc, error) {
	out := &v1beta1.CustomResourceDefinition{}
	scheme := runtime.NewScheme()
	if err := apiextensions.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := v1beta1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := scheme.Convert(&crd, out, nil); err != nil {
		return nil, err
	}
	_, err := c.ApiextensionsInterface().ApiextensionsV1beta1().CustomResourceDefinitions().Create(context.TODO(), out, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	return buildCRDCleanupFunc(c, crd.GetName()), nil
}

func createV1CRD(c operatorclient.ClientInterface, crd apiextensionsv1.CustomResourceDefinition) (cleanupFunc, error) {
	out := &apiextensionsv1.CustomResourceDefinition{}
	scheme := runtime.NewScheme()
	if err := apiextensions.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := apiextensionsv1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := scheme.Convert(&crd, out, nil); err != nil {
		return nil, err
	}
	_, err := c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Create(context.TODO(), out, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	return buildCRDCleanupFunc(c, crd.GetName()), nil
}

func newNginxDeployment(name string) appsv1.DeploymentSpec {
	return appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": name,
			},
		},
		Replicas: &singleInstance,
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": name,
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  genName("nginx"),
						Image: *dummyImage,
						Ports: []corev1.ContainerPort{
							{
								ContainerPort: 80,
							},
						},
						ImagePullPolicy: corev1.PullIfNotPresent,
					},
				},
			},
		},
	}
}

type mockGroupVersionKind struct {
	Name             string
	MockGroupVersion string
	MockKinds        []string
	Port             int
}

func newMockExtServerDeployment(labelName string, mGVKs []mockGroupVersionKind) appsv1.DeploymentSpec {

	// Create the list of containers
	containers := []corev1.Container{}
	for _, mGVK := range mGVKs {
		containers = append(containers, corev1.Container{
			Name:    genName(mGVK.Name),
			Image:   "quay.io/coreos/mock-extension-apiserver:master",
			Command: []string{"/bin/mock-extension-apiserver"},
			Args: []string{
				"-v=4",
				"--mock-kinds",
				strings.Join(mGVK.MockKinds, ","),
				"--mock-group-version",
				mGVK.MockGroupVersion,
				"--secure-port",
				strconv.Itoa(mGVK.Port),
				"--debug",
			},
			Ports: []corev1.ContainerPort{
				{
					ContainerPort: int32(mGVK.Port),
				},
			},
			ImagePullPolicy: corev1.PullIfNotPresent,
		})
	}
	return appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": labelName,
			},
		},
		Replicas: &singleInstance,
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": labelName,
				},
			},
			Spec: corev1.PodSpec{
				Containers: containers,
			},
		},
	}
}

type csvConditionChecker func(csv *v1alpha1.ClusterServiceVersion) bool

func buildCSVConditionChecker(phases ...v1alpha1.ClusterServiceVersionPhase) csvConditionChecker {
	return func(csv *v1alpha1.ClusterServiceVersion) bool {
		conditionMet := false
		for _, phase := range phases {
			conditionMet = conditionMet || csv.Status.Phase == phase
		}
		return conditionMet
	}
}

func buildCSVReasonChecker(reasons ...v1alpha1.ConditionReason) csvConditionChecker {
	return func(csv *v1alpha1.ClusterServiceVersion) bool {
		conditionMet := false
		for _, reason := range reasons {
			conditionMet = conditionMet || csv.Status.Reason == reason
		}
		return conditionMet
	}
}

var csvPendingChecker = buildCSVConditionChecker(v1alpha1.CSVPhasePending)
var csvSucceededChecker = buildCSVConditionChecker(v1alpha1.CSVPhaseSucceeded)
var csvReplacingChecker = buildCSVConditionChecker(v1alpha1.CSVPhaseReplacing, v1alpha1.CSVPhaseDeleting)
var csvFailedChecker = buildCSVConditionChecker(v1alpha1.CSVPhaseFailed)
var csvAnyChecker = buildCSVConditionChecker(v1alpha1.CSVPhasePending, v1alpha1.CSVPhaseSucceeded, v1alpha1.CSVPhaseReplacing, v1alpha1.CSVPhaseDeleting, v1alpha1.CSVPhaseFailed)
var csvCopiedChecker = buildCSVReasonChecker(v1alpha1.CSVReasonCopied)

func fetchCSV(c versioned.Interface, name, namespace string, checker csvConditionChecker) (*v1alpha1.ClusterServiceVersion, error) {
	var fetchedCSV *v1alpha1.ClusterServiceVersion
	var err error

	Eventually(func() (bool, error) {
		fetchedCSV, err = c.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		ctx.Ctx().Logf("%s (%s): %s", fetchedCSV.Status.Phase, fetchedCSV.Status.Reason, fetchedCSV.Status.Message)
		return checker(fetchedCSV), nil
	}).Should(BeTrue())

	if err != nil {
		ctx.Ctx().Logf("never got correct status: %#v", fetchedCSV.Status)
	}
	return fetchedCSV, err
}

func awaitCSV(c versioned.Interface, namespace, name string, checker csvConditionChecker) (*v1alpha1.ClusterServiceVersion, error) {
	var fetched *v1alpha1.ClusterServiceVersion
	var err error

	Eventually(func() (bool, error) {
		fetched, err = c.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		ctx.Ctx().Logf("%s - %s (%s): %s", name, fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message)
		return checker(fetched), nil
	}).Should(BeTrue())

	if err != nil {
		ctx.Ctx().Logf("never got correct status: %#v", fetched.Status)
	}
	return fetched, err
}

func waitForDeployment(c operatorclient.ClientInterface, name string) error {
	return wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		_, err := c.GetDeployment(testNamespace, name)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
}

func waitForDeploymentToDelete(c operatorclient.ClientInterface, name string) error {
	var err error

	Eventually(func() (bool, error) {
		ctx.Ctx().Logf("waiting for deployment %s to delete", name)
		_, err := c.GetDeployment(testNamespace, name)
		if errors.IsNotFound(err) {
			ctx.Ctx().Logf("deleted %s", name)
			return true, nil
		}
		if err != nil {
			ctx.Ctx().Logf("err trying to delete %s: %s", name, err)
			return false, err
		}
		return false, nil
	}).Should(BeTrue())
	return err
}

func csvExists(c versioned.Interface, name string) bool {

	fetched, err := c.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Get(context.TODO(), name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return false
	}
	ctx.Ctx().Logf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message)
	if err != nil {
		return true
	}
	return true
}

func deleteLegacyAPIResources(desc v1alpha1.APIServiceDescription) {
	c := newKubeClient()

	apiServiceName := fmt.Sprintf("%s.%s", desc.Version, desc.Group)

	err := c.DeleteService(testNamespace, strings.Replace(apiServiceName, ".", "-", -1), &metav1.DeleteOptions{})
	Expect(err).ShouldNot(HaveOccurred())

	err = c.DeleteSecret(testNamespace, apiServiceName+"-cert", &metav1.DeleteOptions{})
	Expect(err).ShouldNot(HaveOccurred())

	err = c.DeleteRole(testNamespace, apiServiceName+"-cert", &metav1.DeleteOptions{})
	Expect(err).ShouldNot(HaveOccurred())

	err = c.DeleteRoleBinding(testNamespace, apiServiceName+"-cert", &metav1.DeleteOptions{})
	Expect(err).ShouldNot(HaveOccurred())

	err = c.DeleteClusterRoleBinding(apiServiceName+"-system:auth-delegator", &metav1.DeleteOptions{})
	Expect(err).ShouldNot(HaveOccurred())

	err = c.DeleteRoleBinding("kube-system", apiServiceName+"-auth-reader", &metav1.DeleteOptions{})
	Expect(err).ShouldNot(HaveOccurred())
}

func createLegacyAPIResources(csv *v1alpha1.ClusterServiceVersion, desc v1alpha1.APIServiceDescription) {
	c := newKubeClient()

	apiServiceName := fmt.Sprintf("%s.%s", desc.Version, desc.Group)

	// Attempt to create the legacy service
	service := corev1.Service{}
	service.SetName(strings.Replace(apiServiceName, ".", "-", -1))
	service.SetNamespace(testNamespace)
	if csv != nil {
		err := ownerutil.AddOwnerLabels(&service, csv)
		Expect(err).ShouldNot(HaveOccurred())
	}

	service.Spec.Ports = []corev1.ServicePort{{Port: 433, TargetPort: intstr.FromInt(443)}}
	_, err := c.CreateService(&service)
	Expect(err).ShouldNot(HaveOccurred())

	// Attempt to create the legacy secret
	secret := corev1.Secret{}
	secret.SetName(apiServiceName + "-cert")
	secret.SetNamespace(testNamespace)
	if csv != nil {
		err = ownerutil.AddOwnerLabels(&secret, csv)
		Expect(err).ShouldNot(HaveOccurred())
	}

	_, err = c.CreateSecret(&secret)
	if err != nil && !errors.IsAlreadyExists(err) {
		Expect(err).ShouldNot(HaveOccurred())
	}

	// Attempt to create the legacy secret role
	role := rbacv1.Role{}
	role.SetName(apiServiceName + "-cert")
	role.SetNamespace(testNamespace)
	if csv != nil {
		err = ownerutil.AddOwnerLabels(&role, csv)
		Expect(err).ShouldNot(HaveOccurred())
	}
	_, err = c.CreateRole(&role)
	Expect(err).ShouldNot(HaveOccurred())

	// Attempt to create the legacy secret role binding
	roleBinding := rbacv1.RoleBinding{}
	roleBinding.SetName(apiServiceName + "-cert")
	roleBinding.SetNamespace(testNamespace)
	roleBinding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     role.GetName(),
	}
	if csv != nil {
		err = ownerutil.AddOwnerLabels(&roleBinding, csv)
		Expect(err).ShouldNot(HaveOccurred())
	}

	_, err = c.CreateRoleBinding(&roleBinding)
	Expect(err).ShouldNot(HaveOccurred())

	// Attempt to create the legacy authDelegatorClusterRoleBinding
	clusterRoleBinding := rbacv1.ClusterRoleBinding{}
	clusterRoleBinding.SetName(apiServiceName + "-system:auth-delegator")
	clusterRoleBinding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "ClusterRole",
		Name:     "system:auth-delegator",
	}
	if csv != nil {
		err = ownerutil.AddOwnerLabels(&clusterRoleBinding, csv)
		Expect(err).ShouldNot(HaveOccurred())
	}
	_, err = c.CreateClusterRoleBinding(&clusterRoleBinding)
	Expect(err).ShouldNot(HaveOccurred())

	// Attempt to create the legacy authReadingRoleBinding
	roleBinding.SetName(apiServiceName + "-auth-reader")
	roleBinding.SetNamespace("kube-system")
	roleBinding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     "extension-apiserver-authentication-reader",
	}
	_, err = c.CreateRoleBinding(&roleBinding)
	Expect(err).ShouldNot(HaveOccurred())
}

func checkLegacyAPIResources(desc v1alpha1.APIServiceDescription, expectedIsNotFound bool) {
	c := newKubeClient()
	apiServiceName := fmt.Sprintf("%s.%s", desc.Version, desc.Group)

	// Attempt to create the legacy service
	_, err := c.GetService(testNamespace, strings.Replace(apiServiceName, ".", "-", -1))
	Expect(errors.IsNotFound(err)).Should(Equal(expectedIsNotFound))

	// Attempt to create the legacy secret
	_, err = c.GetSecret(testNamespace, apiServiceName+"-cert")
	Expect(errors.IsNotFound(err)).Should(Equal(expectedIsNotFound))

	// Attempt to create the legacy secret role
	_, err = c.GetRole(testNamespace, apiServiceName+"-cert")
	Expect(errors.IsNotFound(err)).Should(Equal(expectedIsNotFound))

	// Attempt to create the legacy secret role binding
	_, err = c.GetRoleBinding(testNamespace, apiServiceName+"-cert")
	Expect(errors.IsNotFound(err)).Should(Equal(expectedIsNotFound))

	// Attempt to create the legacy authDelegatorClusterRoleBinding
	_, err = c.GetClusterRoleBinding(apiServiceName + "-system:auth-delegator")
	Expect(errors.IsNotFound(err)).Should(Equal(expectedIsNotFound))

	// Attempt to create the legacy authReadingRoleBinding
	_, err = c.GetRoleBinding("kube-system", apiServiceName+"-auth-reader")
	Expect(errors.IsNotFound(err)).Should(Equal(expectedIsNotFound))
}

package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	v1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

// Global Variables
const (
	webhookName = "webhook.test.com"
)

var _ = Describe("CSVs with a Webhook", func() {
	var c operatorclient.ClientInterface
	var crc versioned.Interface
	var namespace *corev1.Namespace
	var nsCleanupFunc cleanupFunc
	BeforeEach(func() {
		c = newKubeClient(GinkgoT())
		crc = newCRClient(GinkgoT())
		namespace, nsCleanupFunc = newNamespace(GinkgoT(), c, genName("webhook-test-"))
	})
	AfterEach(func() {
		if nsCleanupFunc != nil {
			nsCleanupFunc()
		}
	})
	When("Installed in an OperatorGroup that defines a selector", func() {
		var nsLabels map[string]string
		var cleanupCSV cleanupFunc
		var ogSelector *metav1.LabelSelector
		BeforeEach(func() {
			nsLabels = map[string]string{
				"foo": "bar",
			}
			ogSelector = &metav1.LabelSelector{
				MatchLabels: nsLabels,
			}
			// Add a label to the namespace
			Eventually(func() error {
				var err error
				namespace, err = c.KubernetesInterface().CoreV1().Namespaces().Get(context.TODO(), namespace.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}

				if namespace.GetLabels() == nil {
					namespace.SetLabels(map[string]string{})
				}
				namespace.Labels["foo"] = "bar"

				c.KubernetesInterface().CoreV1().Namespaces().Update(context.TODO(), namespace, metav1.UpdateOptions{})
				return err
			}, time.Minute, 5*time.Second).Should(Succeed())

			og := newOperatorGroup(namespace.Name, genName("selector-og-"), nil, ogSelector, nil, false)
			_, err := crc.OperatorsV1().OperatorGroups(namespace.Name).Create(context.TODO(), og, metav1.CreateOptions{})
			Expect(err).Should(BeNil())
		})
		AfterEach(func() {
			if cleanupCSV != nil {
				cleanupCSV()
			}
		})
		It("The webhook is scoped to the selector", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := v1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    v1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
			}

			csv := createCSVWithWebhook(namespace.GetName(), webhook)
			var err error
			cleanupCSV, err = createCSV(GinkgoT(), c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(GinkgoT(), crc, csv.Name, namespace.Name, csvSucceededChecker)
			Expect(err).Should(BeNil())

			actualWebhook, err := getWebhookWithGenName(c, webhook)
			Expect(err).Should(BeNil())

			Expect(actualWebhook.Webhooks[0].NamespaceSelector).Should(Equal(ogSelector))
		})
	})
	When("Installed in a SingleNamespace OperatorGroup", func() {
		var cleanupCSV cleanupFunc
		var og *v1.OperatorGroup
		BeforeEach(func() {
			og = newOperatorGroup(namespace.Name, genName("single-namespace-og-"), nil, nil, []string{namespace.Name}, false)
			var err error
			og, err = crc.OperatorsV1().OperatorGroups(namespace.Name).Create(context.TODO(), og, metav1.CreateOptions{})
			Expect(err).Should(BeNil())
		})
		AfterEach(func() {
			if cleanupCSV != nil {
				cleanupCSV()
			}
		})
		It("Creates Webhooks scoped to a single namespace", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := v1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    v1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
			}

			csv := createCSVWithWebhook(namespace.GetName(), webhook)
			var err error
			cleanupCSV, err = createCSV(GinkgoT(), c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(GinkgoT(), crc, csv.Name, namespace.Name, csvSucceededChecker)
			Expect(err).Should(BeNil())

			actualWebhook, err := getWebhookWithGenName(c, webhook)
			Expect(err).Should(BeNil())

			ogLabel, err := getOGLabelKey(og)
			require.NoError(GinkgoT(), err)

			expected := &metav1.LabelSelector{
				MatchLabels:      map[string]string{ogLabel: ""},
				MatchExpressions: []metav1.LabelSelectorRequirement(nil),
			}
			Expect(actualWebhook.Webhooks[0].NamespaceSelector).Should(Equal(expected))
		})
		It("Fails to install a CSV if multiple Webhooks share the same name", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := v1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    v1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
			}

			csv := createCSVWithWebhook(namespace.GetName(), webhook)
			csv.Spec.WebhookDefinitions = append(csv.Spec.WebhookDefinitions, webhook)
			var err error
			cleanupCSV, err = createCSV(GinkgoT(), c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(GinkgoT(), crc, csv.Name, namespace.Name, csvFailedChecker)
			Expect(err).Should(BeNil())
		})
		It("Fails if the webhooks intercepts all resources", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := v1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    v1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
				Rules: []admissionregistrationv1.RuleWithOperations{
					admissionregistrationv1.RuleWithOperations{
						Operations: []admissionregistrationv1.OperationType{},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{"*"},
							APIVersions: []string{"*"},
							Resources:   []string{"*"},
						},
					},
				},
			}

			csv := createCSVWithWebhook(namespace.GetName(), webhook)

			var err error
			cleanupCSV, err = createCSV(GinkgoT(), c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			failedCSV, err := fetchCSV(GinkgoT(), crc, csv.Name, namespace.Name, csvFailedChecker)
			Expect(err).Should(BeNil())
			Expect(failedCSV.Status.Message).Should(Equal("Webhook rules cannot include all groups"))
		})
		It("Fails if the webhook intercepts OLM resources", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := v1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    v1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
				Rules: []admissionregistrationv1.RuleWithOperations{
					admissionregistrationv1.RuleWithOperations{
						Operations: []admissionregistrationv1.OperationType{},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{"operators.coreos.com"},
							APIVersions: []string{"*"},
							Resources:   []string{"*"},
						},
					},
				},
			}

			csv := createCSVWithWebhook(namespace.GetName(), webhook)

			var err error
			cleanupCSV, err = createCSV(GinkgoT(), c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			failedCSV, err := fetchCSV(GinkgoT(), crc, csv.Name, namespace.Name, csvFailedChecker)
			Expect(err).Should(BeNil())
			Expect(failedCSV.Status.Message).Should(Equal("Webhook rules cannot include the OLM group"))
		})
		It("Fails if webhook intercepts Admission Webhook resources", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := v1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    v1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
				Rules: []admissionregistrationv1.RuleWithOperations{
					admissionregistrationv1.RuleWithOperations{
						Operations: []admissionregistrationv1.OperationType{},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{"admissionregistration.k8s.io"},
							APIVersions: []string{"*"},
							Resources:   []string{"*"},
						},
					},
				},
			}

			csv := createCSVWithWebhook(namespace.GetName(), webhook)

			var err error
			cleanupCSV, err = createCSV(GinkgoT(), c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			failedCSV, err := fetchCSV(GinkgoT(), crc, csv.Name, namespace.Name, csvFailedChecker)
			Expect(err).Should(BeNil())
			Expect(failedCSV.Status.Message).Should(Equal("Webhook rules cannot include MutatingWebhookConfiguration or ValidatingWebhookConfiguration resources"))
		})
		It("Succeeds if the webhook intercepts non Admission Webhook resources in admissionregistration group", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := v1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    v1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
				Rules: []admissionregistrationv1.RuleWithOperations{
					admissionregistrationv1.RuleWithOperations{
						Operations: []admissionregistrationv1.OperationType{
							admissionregistrationv1.OperationAll,
						},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{"admissionregistration.k8s.io"},
							APIVersions: []string{"*"},
							Resources:   []string{"SomeOtherResource"},
						},
					},
				},
			}

			csv := createCSVWithWebhook(namespace.GetName(), webhook)

			var err error
			cleanupCSV, err = createCSV(GinkgoT(), c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(GinkgoT(), crc, csv.Name, namespace.Name, csvSucceededChecker)
			Expect(err).Should(BeNil())
		})
		It("Can be installed and upgraded successfully", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := v1alpha1.WebhookDescription{
				GenerateName:            "webhook.test.com",
				Type:                    v1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
				Rules: []admissionregistrationv1.RuleWithOperations{
					admissionregistrationv1.RuleWithOperations{
						Operations: []admissionregistrationv1.OperationType{
							admissionregistrationv1.OperationAll,
						},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{"admissionregistration.k8s.io"},
							APIVersions: []string{"*"},
							Resources:   []string{"SomeOtherResource"},
						},
					},
				},
			}

			csv := createCSVWithWebhook(namespace.GetName(), webhook)

			_, err := createCSV(GinkgoT(), c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())
			// cleanup by upgrade

			_, err = fetchCSV(GinkgoT(), crc, csv.Name, namespace.Name, csvSucceededChecker)
			Expect(err).Should(BeNil())

			_, err = getWebhookWithGenName(c, webhook)
			Expect(err).Should(BeNil())

			// Update the CSV so it it replaces the existing CSV
			csv.Spec.Replaces = csv.GetName()
			csv.Name = genName("csv-")
			previousWebhookName := webhook.GenerateName
			webhook.GenerateName = "webhook2.test.com"
			csv.Spec.WebhookDefinitions[0] = webhook
			cleanupCSV, err = createCSV(GinkgoT(), c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(GinkgoT(), crc, csv.GetName(), namespace.Name, csvSucceededChecker)
			Expect(err).Should(BeNil())

			_, err = getWebhookWithGenName(c, webhook)
			Expect(err).Should(BeNil())

			// Make sure old resources are cleaned up.
			err = waitForCSVToDelete(GinkgoT(), crc, csv.Spec.Replaces)
			Expect(err).Should(BeNil())

			err = waitForNotFound(GinkgoT(), func() error {
				_, err = c.KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(context.TODO(), previousWebhookName, metav1.GetOptions{})
				return err
			})
			Expect(err).Should(BeNil())
		})
		It("Is updated when the CAs expire", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := v1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    v1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
			}

			csv := createCSVWithWebhook(namespace.GetName(), webhook)

			var err error
			cleanupCSV, err = createCSV(GinkgoT(), c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			fetchedCSV, err := fetchCSV(GinkgoT(), crc, csv.Name, namespace.Name, csvSucceededChecker)
			Expect(err).Should(BeNil())

			actualWebhook, err := getWebhookWithGenName(c, webhook)
			Expect(err).Should(BeNil())

			oldWebhookCABundle := actualWebhook.Webhooks[0].ClientConfig.CABundle

			// Get the deployment
			dep, err := c.KubernetesInterface().AppsV1().Deployments(namespace.Name).Get(context.TODO(), csv.Spec.WebhookDefinitions[0].DeploymentName, metav1.GetOptions{})
			Expect(err).Should(BeNil())

			//Store the ca sha annotation
			oldCAAnnotation, ok := dep.Spec.Template.GetAnnotations()[install.OLMCAHashAnnotationKey]
			Expect(ok).Should(BeTrue())

			// Induce a cert rotation
			now := metav1.Now()
			fetchedCSV.Status.CertsLastUpdated = &now
			fetchedCSV.Status.CertsRotateAt = &now
			fetchedCSV, err = crc.OperatorsV1alpha1().ClusterServiceVersions(namespace.Name).UpdateStatus(context.TODO(), fetchedCSV, metav1.UpdateOptions{})
			Expect(err).Should(BeNil())
			_, err = fetchCSV(GinkgoT(), crc, csv.Name, namespace.Name, func(csv *v1alpha1.ClusterServiceVersion) bool {
				// Should create deployment
				dep, err = c.GetDeployment(namespace.Name, csv.Spec.WebhookDefinitions[0].DeploymentName)
				Expect(err).Should(BeNil())

				// Should have a new ca hash annotation
				newCAAnnotation, ok := dep.Spec.Template.GetAnnotations()[install.OLMCAHashAnnotationKey]
				Expect(ok).Should(BeTrue())

				if newCAAnnotation != oldCAAnnotation {
					// Check for success
					return csvSucceededChecker(csv)
				}

				return false
			})
			Expect(err).Should(BeNil())

			// get new webhook
			actualWebhook, err = getWebhookWithGenName(c, webhook)
			Expect(err).Should(BeNil())

			newWebhookCABundle := actualWebhook.Webhooks[0].ClientConfig.CABundle
			Expect(newWebhookCABundle).ShouldNot(Equal(oldWebhookCABundle))
		})
	})
	When("Installed in a Global OperatorGroup", func() {
		var cleanupCSV cleanupFunc
		BeforeEach(func() {
			og := newOperatorGroup(namespace.Name, genName("global-og-"), nil, nil, []string{}, false)
			og, err := crc.OperatorsV1().OperatorGroups(namespace.Name).Create(context.TODO(), og, metav1.CreateOptions{})
			Expect(err).Should(BeNil())
		})
		AfterEach(func() {
			if cleanupCSV != nil {
				cleanupCSV()
			}
		})
		It("The webhook is scoped to all namespaces", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := v1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    v1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
			}

			csv := createCSVWithWebhook(namespace.GetName(), webhook)

			var err error
			cleanupCSV, err = createCSV(GinkgoT(), c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(GinkgoT(), crc, csv.Name, namespace.Name, csvSucceededChecker)
			Expect(err).Should(BeNil())
			actualWebhook, err := getWebhookWithGenName(c, webhook)
			Expect(err).Should(BeNil())

			expected := &metav1.LabelSelector{
				MatchLabels:      map[string]string(nil),
				MatchExpressions: []metav1.LabelSelectorRequirement(nil),
			}
			Expect(actualWebhook.Webhooks[0].NamespaceSelector).Should(Equal(expected))
		})
	})
	It("Allows multiple installs of the same webhook", func() {
		var csv v1alpha1.ClusterServiceVersion
		namespace1, ns1CleanupFunc := newNamespace(GinkgoT(), c, genName("webhook-test-"))
		defer ns1CleanupFunc()

		namespace2, ns2CleanupFunc := newNamespace(GinkgoT(), c, genName("webhook-test-"))
		defer ns2CleanupFunc()

		og1 := newOperatorGroup(namespace1.Name, genName("test-og-"), nil, nil, []string{"test-go-"}, false)
		og1, err := crc.OperatorsV1().OperatorGroups(namespace1.Name).Create(context.TODO(), og1, metav1.CreateOptions{})
		Expect(err).Should(BeNil())

		og2 := newOperatorGroup(namespace2.Name, genName("test-og-"), nil, nil, []string{"test-go-"}, false)
		og2, err = crc.OperatorsV1().OperatorGroups(namespace2.Name).Create(context.TODO(), og2, metav1.CreateOptions{})
		Expect(err).Should(BeNil())

		sideEffect := admissionregistrationv1.SideEffectClassNone
		webhook := v1alpha1.WebhookDescription{
			GenerateName:            webhookName,
			Type:                    v1alpha1.ValidatingAdmissionWebhook,
			DeploymentName:          genName("webhook-dep-"),
			ContainerPort:           443,
			AdmissionReviewVersions: []string{"v1beta1", "v1"},
			SideEffects:             &sideEffect,
		}

		csv = createCSVWithWebhook(namespace.GetName(), webhook)

		csv.Namespace = namespace1.GetName()
		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, namespace1.Name, false, false)
		Expect(err).Should(BeNil())
		defer cleanupCSV()

		_, err = fetchCSV(GinkgoT(), crc, csv.Name, namespace1.Name, csvSucceededChecker)
		Expect(err).Should(BeNil())

		csv.Namespace = namespace2.Name
		cleanupCSV, err = createCSV(GinkgoT(), c, crc, csv, namespace2.Name, false, false)
		Expect(err).Should(BeNil())
		defer cleanupCSV()

		_, err = fetchCSV(GinkgoT(), crc, csv.Name, namespace2.Name, csvSucceededChecker)
		Expect(err).Should(BeNil())

		webhooks, err := c.KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{})
		Expect(err).Should(BeNil())
		count := 0
		for _, w := range webhooks.Items {
			if strings.HasPrefix(w.GetName(), webhook.GenerateName) {
				count++
			}
		}
		Expect(count).Should(Equal(2))
	})
})

func getWebhookWithGenName(c operatorclient.ClientInterface, desc v1alpha1.WebhookDescription) (*admissionregistrationv1.ValidatingWebhookConfiguration, error) {
	webhookSelector := labels.SelectorFromSet(map[string]string{install.WebhookDescKey: desc.GenerateName}).String()
	existingWebhooks, err := c.KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{LabelSelector: webhookSelector})
	if err != nil {
		return nil, err
	}

	if len(existingWebhooks.Items) > 0 {
		return &existingWebhooks.Items[0], nil
	}
	return nil, fmt.Errorf("Could not find Webhook")
}

func createCSVWithWebhook(namespace string, webhookDesc v1alpha1.WebhookDescription) v1alpha1.ClusterServiceVersion {
	return v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      genName("webhook-csv-"),
			Namespace: namespace,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			WebhookDefinitions: []v1alpha1.WebhookDescription{
				webhookDesc,
			},
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
			InstallStrategy: newNginxInstallStrategy(webhookDesc.DeploymentName, nil, nil),
		},
	}
}

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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	v1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
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
	var nsLabels map[string]string
	BeforeEach(func() {
		c = newKubeClient()
		crc = newCRClient()
		nsLabels = map[string]string{
			"foo": "bar",
		}
		namespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   genName("webhook-test-"),
				Labels: nsLabels,
			},
		}

		var err error
		namespace, err = c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), namespace, metav1.CreateOptions{})
		Expect(err).Should(BeNil())
		Expect(namespace).ShouldNot(BeNil())

		nsCleanupFunc = func() {
			err := c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), namespace.GetName(), metav1.DeleteOptions{})
			Expect(err).Should(BeNil())
		}
	})
	AfterEach(func() {
		if nsCleanupFunc != nil {
			nsCleanupFunc()
		}
	})
	When("Installed in an OperatorGroup that defines a selector", func() {
		var cleanupCSV cleanupFunc
		var ogSelector *metav1.LabelSelector
		BeforeEach(func() {
			ogSelector = &metav1.LabelSelector{
				MatchLabels: nsLabels,
			}

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
			cleanupCSV, err = createCSV(c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(crc, csv.Name, namespace.Name, csvSucceededChecker)
			Expect(err).Should(BeNil())

			actualWebhook, err := getWebhookWithGenerateName(c, webhook.GenerateName)
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
			cleanupCSV, err = createCSV(c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(crc, csv.Name, namespace.Name, csvSucceededChecker)
			Expect(err).Should(BeNil())

			actualWebhook, err := getWebhookWithGenerateName(c, webhook.GenerateName)
			Expect(err).Should(BeNil())

			ogLabel, err := getOGLabelKey(og)
			require.NoError(GinkgoT(), err)

			expected := &metav1.LabelSelector{
				MatchLabels:      map[string]string{ogLabel: ""},
				MatchExpressions: []metav1.LabelSelectorRequirement(nil),
			}
			Expect(actualWebhook.Webhooks[0].NamespaceSelector).Should(Equal(expected))

			// Ensure that changes to the WebhookDescription within the CSV trigger an update to on cluster resources
			changedGenerateName := webhookName + "-changed"
			Eventually(func() error {
				existingCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace.Name).Get(context.TODO(), csv.GetName(), metav1.GetOptions{})
				if err != nil {
					return err
				}
				existingCSV.Spec.WebhookDefinitions[0].GenerateName = changedGenerateName

				existingCSV, err = crc.OperatorsV1alpha1().ClusterServiceVersions(namespace.Name).Update(context.TODO(), existingCSV, metav1.UpdateOptions{})
				return err
			}, time.Minute, 5*time.Second).Should(Succeed())
			Eventually(func() bool {
				// Previous Webhook should be deleted
				_, err = getWebhookWithGenerateName(c, webhookName)
				if err != nil && err.Error() != "NotFound" {
					return false
				}

				// Current Webhook should exist
				_, err = getWebhookWithGenerateName(c, changedGenerateName)
				if err != nil {
					return false
				}

				return true
			}, time.Minute, 5*time.Second).Should(BeTrue())
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
			cleanupCSV, err = createCSV(c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(crc, csv.Name, namespace.Name, csvFailedChecker)
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
			cleanupCSV, err = createCSV(c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			failedCSV, err := fetchCSV(crc, csv.Name, namespace.Name, csvFailedChecker)
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
			cleanupCSV, err = createCSV(c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			failedCSV, err := fetchCSV(crc, csv.Name, namespace.Name, csvFailedChecker)
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
			cleanupCSV, err = createCSV(c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			failedCSV, err := fetchCSV(crc, csv.Name, namespace.Name, csvFailedChecker)
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
			cleanupCSV, err = createCSV(c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(crc, csv.Name, namespace.Name, csvSucceededChecker)
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

			_, err := createCSV(c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())
			// cleanup by upgrade

			_, err = fetchCSV(crc, csv.Name, namespace.Name, csvSucceededChecker)
			Expect(err).Should(BeNil())

			_, err = getWebhookWithGenerateName(c, webhook.GenerateName)
			Expect(err).Should(BeNil())

			// Update the CSV so it it replaces the existing CSV
			csv.Spec.Replaces = csv.GetName()
			csv.Name = genName("csv-")
			previousWebhookName := webhook.GenerateName
			webhook.GenerateName = "webhook2.test.com"
			csv.Spec.WebhookDefinitions[0] = webhook
			cleanupCSV, err = createCSV(c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(crc, csv.GetName(), namespace.Name, csvSucceededChecker)
			Expect(err).Should(BeNil())

			_, err = getWebhookWithGenerateName(c, webhook.GenerateName)
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
			cleanupCSV, err = createCSV(c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			fetchedCSV, err := fetchCSV(crc, csv.Name, namespace.Name, csvSucceededChecker)
			Expect(err).Should(BeNil())

			actualWebhook, err := getWebhookWithGenerateName(c, webhook.GenerateName)
			Expect(err).Should(BeNil())

			oldWebhookCABundle := actualWebhook.Webhooks[0].ClientConfig.CABundle

			// Get the deployment
			dep, err := c.KubernetesInterface().AppsV1().Deployments(namespace.Name).Get(context.TODO(), csv.Spec.WebhookDefinitions[0].DeploymentName, metav1.GetOptions{})
			Expect(err).Should(BeNil())

			//Store the ca sha annotation
			oldCAAnnotation, ok := dep.Spec.Template.GetAnnotations()[install.OLMCAHashAnnotationKey]
			Expect(ok).Should(BeTrue())

			// Induce a cert rotation
			Eventually(Apply(fetchedCSV, func(csv *operatorsv1alpha1.ClusterServiceVersion) error {
				now := metav1.Now()
				csv.Status.CertsLastUpdated = &now
				csv.Status.CertsRotateAt = &now
				return nil
			})).Should(Succeed())

			_, err = fetchCSV(crc, csv.Name, namespace.Name, func(csv *v1alpha1.ClusterServiceVersion) bool {
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
			actualWebhook, err = getWebhookWithGenerateName(c, webhook.GenerateName)
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
			cleanupCSV, err = createCSV(c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(crc, csv.Name, namespace.Name, csvSucceededChecker)
			Expect(err).Should(BeNil())
			actualWebhook, err := getWebhookWithGenerateName(c, webhook.GenerateName)
			Expect(err).Should(BeNil())

			expected := &metav1.LabelSelector{
				MatchLabels:      map[string]string(nil),
				MatchExpressions: []metav1.LabelSelectorRequirement(nil),
			}
			Expect(actualWebhook.Webhooks[0].NamespaceSelector).Should(Equal(expected))
		})
	})
	It("Allows multiple installs of the same webhook", func() {
		namespace1, cleanupNS1 := newNamespace(c, genName("webhook-test-"))
		defer cleanupNS1()

		namespace2, cleanupNS2 := newNamespace(c, genName("webhook-test-"))
		defer cleanupNS2()

		og1 := newOperatorGroup(namespace1.Name, genName("test-og-"), nil, nil, []string{"test-go-"}, false)
		Eventually(func() error {
			og, err := crc.OperatorsV1().OperatorGroups(namespace1.Name).Create(context.TODO(), og1, metav1.CreateOptions{})
			if err != nil {
				return err
			}

			og1 = og

			return nil
		}).Should(Succeed())

		og2 := newOperatorGroup(namespace2.Name, genName("test-og-"), nil, nil, []string{"test-go-"}, false)
		Eventually(func() error {
			og, err := crc.OperatorsV1().OperatorGroups(namespace2.Name).Create(context.TODO(), og2, metav1.CreateOptions{})
			if err != nil {
				return err
			}

			og2 = og

			return nil
		}).Should(Succeed())

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

		csv.Namespace = namespace1.GetName()
		var cleanupCSV cleanupFunc
		Eventually(func() (err error) {
			cleanupCSV, err = createCSV(c, crc, csv, namespace1.Name, false, false)
			return
		}).Should(Succeed())
		defer cleanupCSV()

		Eventually(func() (err error) {
			_, err = fetchCSV(crc, csv.Name, namespace1.Name, csvSucceededChecker)
			return
		}).Should(Succeed())

		csv.Namespace = namespace2.Name
		Eventually(func() (err error) {
			cleanupCSV, err = createCSV(c, crc, csv, namespace2.Name, false, false)
			return
		}).Should(Succeed())
		defer cleanupCSV()

		Eventually(func() (err error) {
			_, err = fetchCSV(crc, csv.Name, namespace2.Name, csvSucceededChecker)
			return
		}).Should(Succeed())

		Eventually(func() (count int, err error) {
			var webhooks *admissionregistrationv1.ValidatingWebhookConfigurationList
			webhooks, err = c.KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{})
			if err != nil {
				return
			}

			for _, w := range webhooks.Items {
				if strings.HasPrefix(w.GetName(), webhook.GenerateName) {
					count++
				}
			}

			return
		}).Should(Equal(2))
	})
	When("WebhookDescription has conversionCrds field", func() {
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
		It("The conversion crd is updated via webhook", func() {
			// create CRD
			crd1Plural := genName("opgroup")
			crd1 := newV1CRD(crd1Plural)
			cleanupCRD, er := createV1CRD(c, crd1)
			require.NoError(GinkgoT(), er)
			defer cleanupCRD()

			// create another CRD
			crd2Plural := genName("opgroup")
			crd2 := newV1CRD(crd2Plural)
			cleanupCRD2, er := createV1CRD(c, crd2)
			require.NoError(GinkgoT(), er)
			defer cleanupCRD2()

			// describe webhook
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := v1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    v1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
				ConversionCrds:          []string{crd1.GetName(), crd2.GetName()},
			}

			// create csv
			csv := createCSVWithWebhook(namespace.GetName(), webhook)

			var err error
			cleanupCSV, err = createCSV(c, crc, csv, namespace.Name, false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(crc, csv.Name, namespace.Name, csvSucceededChecker)
			Expect(err).Should(BeNil())
			actualWebhook, err := getWebhookWithGenerateName(c, webhook.GenerateName)
			Expect(err).Should(BeNil())

			expected := &metav1.LabelSelector{
				MatchLabels:      map[string]string(nil),
				MatchExpressions: []metav1.LabelSelectorRequirement(nil),
			}
			Expect(actualWebhook.Webhooks[0].NamespaceSelector).Should(Equal(expected))

			expectedUpdatedCrdFields := &apiextensionsv1.CustomResourceConversion{
				Strategy: "Webhook",
			}

			// Read the updated crd1 on cluster into the following crd
			tempCrd1, err := c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crd1.GetName(), metav1.GetOptions{})

			// Read the updated crd1 on cluster into the following crd
			tempCrd2, err := c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crd2.GetName(), metav1.GetOptions{})

			Expect(tempCrd1.Spec.Conversion.Strategy).Should(Equal(expectedUpdatedCrdFields.Strategy))
			Expect(tempCrd2.Spec.Conversion.Strategy).Should(Equal(expectedUpdatedCrdFields.Strategy))

		})
	})
})

func getWebhookWithGenerateName(c operatorclient.ClientInterface, generateName string) (*admissionregistrationv1.ValidatingWebhookConfiguration, error) {
	webhookSelector := labels.SelectorFromSet(map[string]string{install.WebhookDescKey: generateName}).String()
	existingWebhooks, err := c.KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{LabelSelector: webhookSelector})
	if err != nil {
		return nil, err
	}

	if len(existingWebhooks.Items) > 0 {
		return &existingWebhooks.Items[0], nil
	}
	return nil, fmt.Errorf("NotFound")
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

func newV1CRD(plural string) apiextensionsv1.CustomResourceDefinition {
	crd := apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: plural + ".cluster.com",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "cluster.com",
			Scope: "Cluster",
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type:        "object",
							Description: "my crd schema",
						},
					},
				},
				{
					Name:    "v1alpha2",
					Served:  true,
					Storage: false,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type:        "object",
							Description: "my crd schema",
						},
					},
				},
			},
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural:   plural,
				Singular: plural,
				Kind:     plural,
				ListKind: plural + "list",
			},
			PreserveUnknownFields: false,
		},
		Status: apiextensionsv1.CustomResourceDefinitionStatus{
			StoredVersions: []string{"v1alpha1", "v1alpha2"},
		},
	}

	return crd
}

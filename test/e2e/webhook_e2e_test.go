package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"

	v1 "github.com/operator-framework/api/pkg/operators/v1"
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
	var (
		generatedNamespace corev1.Namespace
		c                  operatorclient.ClientInterface
		crc                versioned.Interface
		nsLabels           map[string]string
	)

	BeforeEach(func() {
		c = ctx.Ctx().KubeClient()
		crc = ctx.Ctx().OperatorClient()
		nsLabels = map[string]string{
			"foo": "bar",
		}
		generatedNamespace = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("webhook-e2e-"),
				Labels: map[string]string{
					"foo": "bar",
				},
			},
		}
		Eventually(func() error {
			return ctx.Ctx().Client().Create(context.Background(), &generatedNamespace)
		}).Should(Succeed())
	})

	AfterEach(func() {
		TeardownNamespace(generatedNamespace.GetName())
	})

	When("Installed in an OperatorGroup that defines a selector", func() {
		var cleanupCSV cleanupFunc
		var ogSelector *metav1.LabelSelector

		BeforeEach(func() {
			ogSelector = &metav1.LabelSelector{
				MatchLabels: nsLabels,
			}

			og := newOperatorGroup(generatedNamespace.GetName(), genName("selector-og-"), nil, ogSelector, nil, false)
			_, err := crc.OperatorsV1().OperatorGroups(generatedNamespace.GetName()).Create(context.TODO(), og, metav1.CreateOptions{})
			Expect(err).Should(BeNil())
		})

		AfterEach(func() {
			if cleanupCSV != nil {
				cleanupCSV()
			}
		})

		It("The webhook is scoped to the selector", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := operatorsv1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    operatorsv1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
			}

			csv := createCSVWithWebhook(generatedNamespace.GetName(), webhook)
			var err error
			cleanupCSV, err = createCSV(c, crc, csv, generatedNamespace.GetName(), false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(crc, csv.Name, generatedNamespace.GetName(), csvSucceededChecker)
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
			og = newOperatorGroup(generatedNamespace.GetName(), genName("single-namespace-og-"), nil, nil, []string{generatedNamespace.GetName()}, false)
			var err error
			og, err = crc.OperatorsV1().OperatorGroups(generatedNamespace.GetName()).Create(context.TODO(), og, metav1.CreateOptions{})
			Expect(err).Should(BeNil())
		})

		AfterEach(func() {
			if cleanupCSV != nil {
				cleanupCSV()
			}
		})

		It("Creates Webhooks scoped to a single namespace", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := operatorsv1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    operatorsv1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
			}

			csv := createCSVWithWebhook(generatedNamespace.GetName(), webhook)
			var err error
			cleanupCSV, err = createCSV(c, crc, csv, generatedNamespace.GetName(), false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(crc, csv.Name, generatedNamespace.GetName(), csvSucceededChecker)
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
				existingCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(generatedNamespace.GetName()).Get(context.TODO(), csv.GetName(), metav1.GetOptions{})
				if err != nil {
					return err
				}
				existingCSV.Spec.WebhookDefinitions[0].GenerateName = changedGenerateName

				existingCSV, err = crc.OperatorsV1alpha1().ClusterServiceVersions(generatedNamespace.GetName()).Update(context.TODO(), existingCSV, metav1.UpdateOptions{})
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
				return err == nil
			}, time.Minute, 5*time.Second).Should(BeTrue())
		})
		It("Reuses existing valid certs", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := operatorsv1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    operatorsv1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
			}
			csv := createCSVWithWebhook(generatedNamespace.GetName(), webhook)

			var err error
			cleanupCSV, err = createCSV(c, crc, csv, generatedNamespace.GetName(), false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(crc, csv.Name, generatedNamespace.GetName(), csvSucceededChecker)
			Expect(err).Should(BeNil())

			// Get the existing secret
			webhookSecretName := webhook.DeploymentName + "-service-cert"
			existingSecret, err := c.KubernetesInterface().CoreV1().Secrets(generatedNamespace.GetName()).Get(context.TODO(), webhookSecretName, metav1.GetOptions{})
			require.NoError(GinkgoT(), err)

			// Modify the phase
			Eventually(func() bool {
				fetchedCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(generatedNamespace.GetName()).Get(context.TODO(), csv.GetName(), metav1.GetOptions{})
				if err != nil {
					return false
				}

				fetchedCSV.Status.Phase = operatorsv1alpha1.CSVPhasePending

				_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(generatedNamespace.GetName()).UpdateStatus(context.TODO(), fetchedCSV, metav1.UpdateOptions{})
				return err == nil
			}).Should(BeTrue(), "Unable to set CSV phase to Pending")

			// Wait for webhook-operator to succeed
			_, err = awaitCSV(crc, generatedNamespace.GetName(), csv.GetName(), csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			// Get the updated secret
			updatedSecret, err := c.KubernetesInterface().CoreV1().Secrets(generatedNamespace.GetName()).Get(context.TODO(), webhookSecretName, metav1.GetOptions{})
			require.NoError(GinkgoT(), err)

			require.Equal(GinkgoT(), existingSecret.GetAnnotations()[install.OLMCAHashAnnotationKey], updatedSecret.GetAnnotations()[install.OLMCAHashAnnotationKey])
			require.Equal(GinkgoT(), existingSecret.Data[install.OLMCAPEMKey], updatedSecret.Data[install.OLMCAPEMKey])
		})
		It("Fails to install a CSV if multiple Webhooks share the same name", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := operatorsv1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    operatorsv1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
			}

			csv := createCSVWithWebhook(generatedNamespace.GetName(), webhook)
			csv.Spec.WebhookDefinitions = append(csv.Spec.WebhookDefinitions, webhook)
			var err error
			cleanupCSV, err = createCSV(c, crc, csv, generatedNamespace.GetName(), false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(crc, csv.Name, generatedNamespace.GetName(), csvFailedChecker)
			Expect(err).Should(BeNil())
		})
		It("Fails if the webhooks intercepts all resources", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := operatorsv1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    operatorsv1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{"*"},
							APIVersions: []string{"*"},
							Resources:   []string{"*"},
						},
					},
				},
			}

			csv := createCSVWithWebhook(generatedNamespace.GetName(), webhook)

			var err error
			cleanupCSV, err = createCSV(c, crc, csv, generatedNamespace.GetName(), false, false)
			Expect(err).Should(BeNil())

			failedCSV, err := fetchCSV(crc, csv.Name, generatedNamespace.GetName(), csvFailedChecker)
			Expect(err).Should(BeNil())
			Expect(failedCSV.Status.Message).Should(Equal("webhook rules cannot include all groups"))
		})
		It("Fails if the webhook intercepts OLM resources", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := operatorsv1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    operatorsv1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{"operators.coreos.com"},
							APIVersions: []string{"*"},
							Resources:   []string{"*"},
						},
					},
				},
			}

			csv := createCSVWithWebhook(generatedNamespace.GetName(), webhook)

			var err error
			cleanupCSV, err = createCSV(c, crc, csv, generatedNamespace.GetName(), false, false)
			Expect(err).Should(BeNil())

			failedCSV, err := fetchCSV(crc, csv.Name, generatedNamespace.GetName(), csvFailedChecker)
			Expect(err).Should(BeNil())
			Expect(failedCSV.Status.Message).Should(Equal("webhook rules cannot include the OLM group"))
		})
		It("Fails if webhook intercepts Admission Webhook resources", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := operatorsv1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    operatorsv1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{"admissionregistration.k8s.io"},
							APIVersions: []string{"*"},
							Resources:   []string{"*"},
						},
					},
				},
			}

			csv := createCSVWithWebhook(generatedNamespace.GetName(), webhook)

			var err error
			cleanupCSV, err = createCSV(c, crc, csv, generatedNamespace.GetName(), false, false)
			Expect(err).Should(BeNil())

			failedCSV, err := fetchCSV(crc, csv.Name, generatedNamespace.GetName(), csvFailedChecker)
			Expect(err).Should(BeNil())
			Expect(failedCSV.Status.Message).Should(Equal("webhook rules cannot include MutatingWebhookConfiguration or ValidatingWebhookConfiguration resources"))
		})
		It("Succeeds if the webhook intercepts non Admission Webhook resources in admissionregistration group", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := operatorsv1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    operatorsv1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
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

			csv := createCSVWithWebhook(generatedNamespace.GetName(), webhook)

			var err error
			cleanupCSV, err = createCSV(c, crc, csv, generatedNamespace.GetName(), false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(crc, csv.Name, generatedNamespace.GetName(), csvSucceededChecker)
			Expect(err).Should(BeNil())
		})
		It("Can be installed and upgraded successfully", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := operatorsv1alpha1.WebhookDescription{
				GenerateName:            "webhook.test.com",
				Type:                    operatorsv1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
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

			csv := createCSVWithWebhook(generatedNamespace.GetName(), webhook)

			_, err := createCSV(c, crc, csv, generatedNamespace.GetName(), false, false)
			Expect(err).Should(BeNil())
			// cleanup by upgrade

			_, err = fetchCSV(crc, csv.Name, generatedNamespace.GetName(), csvSucceededChecker)
			Expect(err).Should(BeNil())

			_, err = getWebhookWithGenerateName(c, webhook.GenerateName)
			Expect(err).Should(BeNil())

			// Update the CSV so it it replaces the existing CSV
			csv.Spec.Replaces = csv.GetName()
			csv.Name = genName("csv-")
			previousWebhookName := webhook.GenerateName
			webhook.GenerateName = "webhook2.test.com"
			csv.Spec.WebhookDefinitions[0] = webhook
			cleanupCSV, err = createCSV(c, crc, csv, generatedNamespace.GetName(), false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(crc, csv.GetName(), generatedNamespace.GetName(), csvSucceededChecker)
			Expect(err).Should(BeNil())

			_, err = getWebhookWithGenerateName(c, webhook.GenerateName)
			Expect(err).Should(BeNil())

			// Make sure old resources are cleaned up.
			Eventually(func() bool {
				return csvExists(generatedNamespace.GetName(), crc, csv.Spec.Replaces)
			}).Should(BeFalse())

			// Wait until previous webhook is cleaned up
			Eventually(func() (bool, error) {
				_, err := c.KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(context.TODO(), previousWebhookName, metav1.GetOptions{})
				if errors.IsNotFound(err) {
					return true, nil
				}
				if err != nil {
					return false, err
				}
				return false, nil
			}).Should(BeTrue())
		})
		// issue: https://github.com/operator-framework/operator-lifecycle-manager/issues/2629
		It("[FLAKE] Is updated when the CAs expire", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := operatorsv1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    operatorsv1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
			}

			csv := createCSVWithWebhook(generatedNamespace.GetName(), webhook)

			var err error
			cleanupCSV, err = createCSV(c, crc, csv, generatedNamespace.GetName(), false, false)
			Expect(err).Should(BeNil())

			fetchedCSV, err := fetchCSV(crc, csv.Name, generatedNamespace.GetName(), csvSucceededChecker)
			Expect(err).Should(BeNil())

			actualWebhook, err := getWebhookWithGenerateName(c, webhook.GenerateName)
			Expect(err).Should(BeNil())

			oldWebhookCABundle := actualWebhook.Webhooks[0].ClientConfig.CABundle

			// Get the deployment
			dep, err := c.KubernetesInterface().AppsV1().Deployments(generatedNamespace.GetName()).Get(context.TODO(), csv.Spec.WebhookDefinitions[0].DeploymentName, metav1.GetOptions{})
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

			_, err = fetchCSV(crc, csv.Name, generatedNamespace.GetName(), func(csv *operatorsv1alpha1.ClusterServiceVersion) bool {
				// Should create deployment
				dep, err = c.GetDeployment(generatedNamespace.GetName(), csv.Spec.WebhookDefinitions[0].DeploymentName)
				if err != nil {
					return false
				}

				// Should have a new ca hash annotation
				newCAAnnotation, ok := dep.Spec.Template.GetAnnotations()[install.OLMCAHashAnnotationKey]
				if !ok {
					return false
				}

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
			og := newOperatorGroup(generatedNamespace.GetName(), genName("global-og-"), nil, nil, []string{}, false)
			og, err := crc.OperatorsV1().OperatorGroups(generatedNamespace.GetName()).Create(context.TODO(), og, metav1.CreateOptions{})
			Expect(err).Should(BeNil())
		})

		AfterEach(func() {
			if cleanupCSV != nil {
				cleanupCSV()
			}
		})

		It("The webhook is scoped to all namespaces", func() {
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := operatorsv1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    operatorsv1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
			}

			csv := createCSVWithWebhook(generatedNamespace.GetName(), webhook)

			var err error
			cleanupCSV, err = createCSV(c, crc, csv, generatedNamespace.GetName(), false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(crc, csv.Name, generatedNamespace.GetName(), csvSucceededChecker)
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
		webhook := operatorsv1alpha1.WebhookDescription{
			GenerateName:            webhookName,
			Type:                    operatorsv1alpha1.ValidatingAdmissionWebhook,
			DeploymentName:          genName("webhook-dep-"),
			ContainerPort:           443,
			AdmissionReviewVersions: []string{"v1beta1", "v1"},
			SideEffects:             &sideEffect,
		}

		csv := createCSVWithWebhook(generatedNamespace.GetName(), webhook)

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
	When("Installed from a catalog Source", func() {
		const csvName = "webhook-operator.v0.0.1"
		var cleanupCSV cleanupFunc
		var cleanupCatSrc cleanupFunc
		var cleanupSubscription cleanupFunc

		BeforeEach(func() {
			og := newOperatorGroup(generatedNamespace.GetName(), genName("og-"), nil, nil, []string{}, false)
			_, err := crc.OperatorsV1().OperatorGroups(generatedNamespace.GetName()).Create(context.TODO(), og, metav1.CreateOptions{})
			Expect(err).Should(BeNil())

			// Create a catalogSource which has the webhook-operator
			sourceName := genName("catalog-")
			packageName := "webhook-operator"
			channelName := "alpha"

			catSrcImage := "quay.io/operator-framework/webhook-operator-index"

			// Create gRPC CatalogSource
			source := &operatorsv1alpha1.CatalogSource{
				TypeMeta: metav1.TypeMeta{
					Kind:       operatorsv1alpha1.CatalogSourceKind,
					APIVersion: operatorsv1alpha1.CatalogSourceCRDAPIVersion,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      sourceName,
					Namespace: generatedNamespace.GetName(),
				},
				Spec: operatorsv1alpha1.CatalogSourceSpec{
					SourceType: operatorsv1alpha1.SourceTypeGrpc,
					Image:      catSrcImage + ":0.0.3",
					GrpcPodConfig: &operatorsv1alpha1.GrpcPodConfig{
						SecurityContextConfig: operatorsv1alpha1.Restricted,
					},
				},
			}

			source, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Create(context.TODO(), source, metav1.CreateOptions{})
			require.NoError(GinkgoT(), err)
			cleanupCatSrc = func() {
				require.NoError(GinkgoT(), crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Delete(context.TODO(), source.GetName(), metav1.DeleteOptions{}))
			}

			// Wait for the CatalogSource to be ready
			_, err = fetchCatalogSourceOnStatus(crc, source.GetName(), source.GetNamespace(), catalogSourceRegistryPodSynced)
			require.NoError(GinkgoT(), err)

			// Create a Subscription for the webhook-operator
			subscriptionName := genName("sub-")
			cleanupSubscription := createSubscriptionForCatalog(crc, source.GetNamespace(), subscriptionName, source.GetName(), packageName, channelName, "", operatorsv1alpha1.ApprovalAutomatic)
			defer cleanupSubscription()

			// Wait for webhook-operator v2 csv to succeed
			csv, err := awaitCSV(crc, source.GetNamespace(), csvName, csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			cleanupCSV = buildCSVCleanupFunc(c, crc, *csv, source.GetNamespace(), true, true)
		})

		AfterEach(func() {
			if cleanupCSV != nil {
				cleanupCSV()
			}
			if cleanupCatSrc != nil {
				cleanupCatSrc()
			}
			if cleanupSubscription != nil {
				cleanupSubscription()
			}
		})

		It("Validating, Mutating and Conversion webhooks work as intended", func() {
			// An invalid custom resource is rejected by the validating webhook
			invalidCR := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "webhook.operators.coreos.io/v1",
					"kind":       "webhooktests",
					"metadata": map[string]interface{}{
						"namespace": generatedNamespace.GetName(),
						"name":      "my-cr-1",
					},
					"spec": map[string]interface{}{
						"valid": false,
					},
				},
			}
			expectedErrorMessage := "admission webhook \"vwebhooktest.kb.io\" denied the request: WebhookTest.test.operators.coreos.com \"my-cr-1\" is invalid: spec.schedule: Invalid value: false: Spec.Valid must be true"
			Eventually(func() bool {
				err := c.CreateCustomResource(invalidCR)
				if err == nil || expectedErrorMessage != err.Error() {
					return false
				}
				return true
			}).Should(BeTrue(), "The admission webhook should have rejected the invalid resource")

			// An valid custom resource is acceoted by the validating webhook and the mutating webhook sets the CR's spec.mutate field to true.
			validCR := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "webhook.operators.coreos.io/v1",
					"kind":       "webhooktests",
					"metadata": map[string]interface{}{
						"namespace": generatedNamespace.GetName(),
						"name":      "my-cr-1",
					},
					"spec": map[string]interface{}{
						"valid": true,
					},
				},
			}
			crCleanupFunc, err := createCR(c, validCR, "webhook.operators.coreos.io", "v1", generatedNamespace.GetName(), "webhooktests", "my-cr-1")
			defer crCleanupFunc()
			require.NoError(GinkgoT(), err, "The valid CR should have been approved by the validating webhook")

			// Check that you can get v1 of the webhooktest cr
			v1UnstructuredObject, err := c.GetCustomResource("webhook.operators.coreos.io", "v1", generatedNamespace.GetName(), "webhooktests", "my-cr-1")
			require.NoError(GinkgoT(), err, "Unable to get the v1 of the valid CR")
			v1Object := v1UnstructuredObject.Object
			v1Spec, ok := v1Object["spec"].(map[string]interface{})
			require.True(GinkgoT(), ok, "Unable to get spec of v1 object")
			v1SpecMutate, ok := v1Spec["mutate"].(bool)
			require.True(GinkgoT(), ok, "Unable to get spec.mutate of v1 object")
			v1SpecValid, ok := v1Spec["valid"].(bool)
			require.True(GinkgoT(), ok, "Unable to get spec.valid of v1 object")

			require.True(GinkgoT(), v1SpecMutate, "The mutating webhook should have set the valid CR's spec.mutate field to true")
			require.True(GinkgoT(), v1SpecValid, "The validating webhook should have required that the CR's spec.valid field is true")

			// Check that you can get v2 of the webhooktest cr
			v2UnstructuredObject, err := c.GetCustomResource("webhook.operators.coreos.io", "v2", generatedNamespace.GetName(), "webhooktests", "my-cr-1")
			require.NoError(GinkgoT(), err, "Unable to get the v2 of the valid CR")
			v2Object := v2UnstructuredObject.Object
			v2Spec := v2Object["spec"].(map[string]interface{})
			require.True(GinkgoT(), ok, "Unable to get spec of v2 object")
			v2SpecConversion, ok := v2Spec["conversion"].(map[string]interface{})
			require.True(GinkgoT(), ok, "Unable to get spec.conversion of v2 object")
			v2SpecConversionMutate := v2SpecConversion["mutate"].(bool)
			require.True(GinkgoT(), ok, "Unable to get spec.conversion.mutate of v2 object")
			v2SpecConversionValid := v2SpecConversion["valid"].(bool)
			require.True(GinkgoT(), ok, "Unable to get spec.conversion.valid of v2 object")
			require.True(GinkgoT(), v2SpecConversionMutate)
			require.True(GinkgoT(), v2SpecConversionValid)

			// Check that conversion strategies are disabled after uninstalling the operator.
			err = crc.OperatorsV1alpha1().ClusterServiceVersions(generatedNamespace.GetName()).Delete(context.TODO(), csvName, metav1.DeleteOptions{})
			require.NoError(GinkgoT(), err)

			Eventually(func() error {
				crd, err := c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), "webhooktests.webhook.operators.coreos.io", metav1.GetOptions{})
				if err != nil {
					return err
				}

				if crd.Spec.Conversion.Strategy != apiextensionsv1.NoneConverter {
					return fmt.Errorf("conversion strategy is not NoneConverter")
				}
				if crd.Spec.Conversion.Webhook != nil {
					return fmt.Errorf("webhook is not nil")
				}
				return nil
			}).Should(Succeed())
		})
	})
	When("WebhookDescription has conversionCRDs field", func() {
		var cleanupCSV cleanupFunc

		BeforeEach(func() {
			// global operator group
			og := newOperatorGroup(generatedNamespace.GetName(), genName("global-og-"), nil, nil, []string{}, false)
			og, err := crc.OperatorsV1().OperatorGroups(generatedNamespace.GetName()).Create(context.TODO(), og, metav1.CreateOptions{})
			Expect(err).Should(BeNil())
		})

		AfterEach(func() {
			if cleanupCSV != nil {
				cleanupCSV()
			}
		})

		It("The conversion CRD is not updated via webhook when CSV does not own this CRD", func() {
			// create CRD (crdA)
			crdAPlural := genName("mockcrda")
			crdA := newV1CRD(crdAPlural)
			cleanupCRD, er := createV1CRD(c, crdA)
			require.NoError(GinkgoT(), er)
			defer cleanupCRD()

			// create another CRD (crdB)
			crdBPlural := genName("mockcrdb")
			crdB := newV1CRD(crdBPlural)
			cleanupCRD2, er := createV1CRD(c, crdB)
			require.NoError(GinkgoT(), er)
			defer cleanupCRD2()

			// describe webhook
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := operatorsv1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    operatorsv1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
				ConversionCRDs:          []string{crdA.GetName(), crdB.GetName()},
			}

			ownedCRDDescs := make([]operatorsv1alpha1.CRDDescription, 0)

			// create CSV
			csv := createCSVWithWebhookAndCrds(generatedNamespace.GetName(), webhook, ownedCRDDescs)

			var err error
			cleanupCSV, err = createCSV(c, crc, csv, generatedNamespace.GetName(), false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(crc, csv.Name, generatedNamespace.GetName(), csvSucceededChecker)
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

			// Read the updated crdA on cluster into the following crd
			tempCrdA, err := c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crdA.GetName(), metav1.GetOptions{})

			// Read the updated crdB on cluster into the following crd
			tempCrdB, err := c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crdB.GetName(), metav1.GetOptions{})

			Expect(tempCrdA.Spec.Conversion.Strategy).Should(Equal(expectedUpdatedCrdFields.Strategy))
			Expect(tempCrdB.Spec.Conversion.Strategy).Should(Equal(expectedUpdatedCrdFields.Strategy))

			var expectedTempPort int32 = 443
			expectedConvertPath := "/convert"
			expectedConvertNamespace := "system"

			Expect(tempCrdA.Spec.Conversion.Webhook.ClientConfig.Service.Port).Should(Equal(&expectedTempPort))
			Expect(tempCrdA.Spec.Conversion.Webhook.ClientConfig.Service.Path).Should(Equal(&expectedConvertPath))
			Expect(tempCrdA.Spec.Conversion.Webhook.ClientConfig.Service.Name).Should(Equal("webhook-service"))
			Expect(tempCrdA.Spec.Conversion.Webhook.ClientConfig.Service.Namespace).Should(Equal(expectedConvertNamespace))
		})
		It("The CSV is not created when dealing with conversionCRD and multiple installModes support exists", func() {
			// create CRD (crdA)
			crdAPlural := genName("mockcrda")
			crdA := newV1CRD(crdAPlural)
			cleanupCRD, er := createV1CRD(c, crdA)
			require.NoError(GinkgoT(), er)
			defer cleanupCRD()

			// describe webhook
			sideEffect := admissionregistrationv1.SideEffectClassNone
			webhook := operatorsv1alpha1.WebhookDescription{
				GenerateName:            webhookName,
				Type:                    operatorsv1alpha1.ValidatingAdmissionWebhook,
				DeploymentName:          genName("webhook-dep-"),
				ContainerPort:           443,
				AdmissionReviewVersions: []string{"v1beta1", "v1"},
				SideEffects:             &sideEffect,
				ConversionCRDs:          []string{crdA.GetName()},
			}

			ownedCRDDescs := make([]operatorsv1alpha1.CRDDescription, 0)
			ownedCRDDescs = append(ownedCRDDescs, operatorsv1alpha1.CRDDescription{Name: crdA.GetName(), Version: crdA.Spec.Versions[0].Name, Kind: crdA.Spec.Names.Kind})

			// create CSV
			csv := createCSVWithWebhookAndCrdsAndInvalidInstallModes(generatedNamespace.GetName(), webhook, ownedCRDDescs)

			var err error
			cleanupCSV, err = createCSV(c, crc, csv, generatedNamespace.GetName(), false, false)
			Expect(err).Should(BeNil())

			_, err = fetchCSV(crc, csv.Name, generatedNamespace.GetName(), csvSucceededChecker)
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

			// Read the updated crdA on cluster into the following crd
			tempCrdA, err := c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crdA.GetName(), metav1.GetOptions{})

			Expect(tempCrdA.Spec.Conversion.Strategy).Should(Equal(expectedUpdatedCrdFields.Strategy))

			var expectedTempPort int32 = 443
			expectedConvertPath := "/convert"

			Expect(tempCrdA.Spec.Conversion.Webhook.ClientConfig.Service.Port).Should(Equal(&expectedTempPort))
			Expect(tempCrdA.Spec.Conversion.Webhook.ClientConfig.Service.Path).Should(Equal(&expectedConvertPath))
			// CRD namespace would not be updated, hence conversion webhook won't work for objects of this CRD's Kind
			Expect(tempCrdA.Spec.Conversion.Webhook.ClientConfig.Service.Namespace).ShouldNot(Equal(csv.GetNamespace()))
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

func createCSVWithWebhook(namespace string, webhookDesc operatorsv1alpha1.WebhookDescription) operatorsv1alpha1.ClusterServiceVersion {
	return operatorsv1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       operatorsv1alpha1.ClusterServiceVersionKind,
			APIVersion: operatorsv1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      genName("webhook-csv-"),
			Namespace: namespace,
		},
		Spec: operatorsv1alpha1.ClusterServiceVersionSpec{
			WebhookDefinitions: []operatorsv1alpha1.WebhookDescription{
				webhookDesc,
			},
			InstallModes: []operatorsv1alpha1.InstallMode{
				{
					Type:      operatorsv1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      operatorsv1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      operatorsv1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      operatorsv1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: newNginxInstallStrategy(webhookDesc.DeploymentName, nil, nil),
		},
	}
}

func createCSVWithWebhookAndCrds(namespace string, webhookDesc operatorsv1alpha1.WebhookDescription, ownedCRDDescs []operatorsv1alpha1.CRDDescription) operatorsv1alpha1.ClusterServiceVersion {
	return operatorsv1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       operatorsv1alpha1.ClusterServiceVersionKind,
			APIVersion: operatorsv1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      genName("webhook-csv-"),
			Namespace: namespace,
		},
		Spec: operatorsv1alpha1.ClusterServiceVersionSpec{
			WebhookDefinitions: []operatorsv1alpha1.WebhookDescription{
				webhookDesc,
			},
			CustomResourceDefinitions: operatorsv1alpha1.CustomResourceDefinitions{
				Owned: ownedCRDDescs,
			},
			InstallModes: []operatorsv1alpha1.InstallMode{
				{
					Type:      operatorsv1alpha1.InstallModeTypeOwnNamespace,
					Supported: false,
				},
				{
					Type:      operatorsv1alpha1.InstallModeTypeSingleNamespace,
					Supported: false,
				},
				{
					Type:      operatorsv1alpha1.InstallModeTypeMultiNamespace,
					Supported: false,
				},
				{
					Type:      operatorsv1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: newNginxInstallStrategy(webhookDesc.DeploymentName, nil, nil),
		},
	}
}

func createCSVWithWebhookAndCrdsAndInvalidInstallModes(namespace string, webhookDesc operatorsv1alpha1.WebhookDescription, ownedCRDDescs []operatorsv1alpha1.CRDDescription) operatorsv1alpha1.ClusterServiceVersion {
	return operatorsv1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       operatorsv1alpha1.ClusterServiceVersionKind,
			APIVersion: operatorsv1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      genName("webhook-csv-"),
			Namespace: namespace,
		},
		Spec: operatorsv1alpha1.ClusterServiceVersionSpec{
			WebhookDefinitions: []operatorsv1alpha1.WebhookDescription{
				webhookDesc,
			},
			CustomResourceDefinitions: operatorsv1alpha1.CustomResourceDefinitions{
				Owned: ownedCRDDescs,
			},
			InstallModes: []operatorsv1alpha1.InstallMode{
				{
					Type:      operatorsv1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      operatorsv1alpha1.InstallModeTypeSingleNamespace,
					Supported: false,
				},
				{
					Type:      operatorsv1alpha1.InstallModeTypeMultiNamespace,
					Supported: false,
				},
				{
					Type:      operatorsv1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: newNginxInstallStrategy(webhookDesc.DeploymentName, nil, nil),
		},
	}
}

func newV1CRD(plural string) apiextensionsv1.CustomResourceDefinition {
	path := "/convert"
	var port int32 = 443
	var min float64 = 2
	var max float64 = 256
	crd := apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: plural + ".cluster.com",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "cluster.com",
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"spec": {
									Type:        "object",
									Description: "Spec of a test object.",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"scalar": {
											Type:        "number",
											Description: "Scalar value that should have a min and max.",
											Minimum:     &min,
											Maximum:     &max,
										},
									},
								},
							},
						},
					},
				},
				{
					Name:    "v1alpha2",
					Served:  true,
					Storage: false,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"spec": {
									Type:        "object",
									Description: "Spec of a test object.",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"scalar": {
											Type:        "number",
											Description: "Scalar value that should have a min and max.",
											Minimum:     &min,
											Maximum:     &max,
										},
									},
								},
							},
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
			Conversion: &apiextensionsv1.CustomResourceConversion{
				Strategy: "Webhook",
				Webhook: &apiextensionsv1.WebhookConversion{
					ClientConfig: &apiextensionsv1.WebhookClientConfig{
						Service: &apiextensionsv1.ServiceReference{
							Namespace: "system",
							Name:      "webhook-service",
							Path:      &path,
							Port:      &port,
						},
					},
					ConversionReviewVersions: []string{"v1", "v1beta1"},
				},
			},
		},
		Status: apiextensionsv1.CustomResourceDefinitionStatus{
			StoredVersions: []string{"v1alpha1", "v1alpha2"},
		},
	}

	return crd
}

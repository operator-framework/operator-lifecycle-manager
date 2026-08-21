package catalog

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextensionsv1client "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apiextensionsv1beta1client "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	listersv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/internal/alongside"
	crdlib "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/crd"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

// Stepper manages cluster interactions based on the step.
type Stepper interface {
	Status() (v1alpha1.StepStatus, error)
}

// StepperFunc fulfills the Stepper interface.
type StepperFunc func() (v1alpha1.StepStatus, error)

func (s StepperFunc) Status() (v1alpha1.StepStatus, error) {
	return s()
}

// Builder holds clients and data structures required for the StepBuilder to work
// Builder attributes are not to meant to be accessed outside the StepBuilder method
type builder struct {
	plan             *v1alpha1.InstallPlan
	csvLister        listersv1alpha1.ClusterServiceVersionLister
	opclient         operatorclient.ClientInterface
	attenuatedClient operatorclient.ClientInterface
	olmClient        versioned.Interface
	dynamicClient    dynamic.Interface
	manifestResolver ManifestResolver
	logger           logrus.FieldLogger
	eventRecorder    record.EventRecorder

	annotator alongside.Annotator
}

func newBuilder(plan *v1alpha1.InstallPlan, csvLister listersv1alpha1.ClusterServiceVersionLister, opclient operatorclient.ClientInterface, attenuatedClient operatorclient.ClientInterface, olmClient versioned.Interface, dynamicClient dynamic.Interface, manifestResolver ManifestResolver, logger logrus.FieldLogger, er record.EventRecorder) *builder {
	return &builder{
		plan:             plan,
		csvLister:        csvLister,
		opclient:         opclient,
		attenuatedClient: attenuatedClient,
		olmClient:        olmClient,
		dynamicClient:    dynamicClient,
		manifestResolver: manifestResolver,
		logger:           logger,
		eventRecorder:    er,
	}
}

type notSupportedStepperErr struct {
	message string
}

func (n notSupportedStepperErr) Error() string {
	return n.message
}

// step is a factory that creates StepperFuncs based on the install plan step Kind.
func (b *builder) create(step v1alpha1.Step) (Stepper, error) {
	manifest, err := b.manifestResolver.ManifestForStep(&step)
	if err != nil {
		return nil, err
	}

	switch step.Resource.Kind {
	case crdKind:
		version, err := crdlib.Version(&manifest)
		if err != nil {
			return nil, err
		}

		switch version {
		case crdlib.V1Version:
			return b.NewCRDV1Step(b.opclient.ApiextensionsInterface().ApiextensionsV1(), &step, manifest), nil
		case crdlib.V1Beta1Version:
			return b.NewCRDV1Beta1Step(b.opclient.ApiextensionsInterface().ApiextensionsV1beta1(), &step, manifest), nil
		}
	case resolver.BundleSecretKind:
		return b.NewBundleSecretStep(&step, manifest), nil
	}
	return nil, notSupportedStepperErr{fmt.Sprintf("stepper interface does not support %s", step.Resource.Kind)}
}

func (b *builder) NewCRDV1Step(client apiextensionsv1client.ApiextensionsV1Interface, step *v1alpha1.Step, manifest string) StepperFunc {
	return func() (v1alpha1.StepStatus, error) {
		switch step.Status {
		case v1alpha1.StepStatusPresent:
			return v1alpha1.StepStatusPresent, nil
		case v1alpha1.StepStatusCreated:
			return v1alpha1.StepStatusCreated, nil
		case v1alpha1.StepStatusWaitingForAPI:
			crd, err := client.CustomResourceDefinitions().Get(context.TODO(), step.Resource.Name, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return v1alpha1.StepStatusNotPresent, nil
				}
				return v1alpha1.StepStatusNotPresent, errors.Wrapf(err, "error finding the %s CRD", crd.Name)
			}
			established, namesAccepted := false, false
			for _, cdt := range crd.Status.Conditions {
				switch cdt.Type {
				case apiextensionsv1.Established:
					if cdt.Status == apiextensionsv1.ConditionTrue {
						established = true
					}
				case apiextensionsv1.NamesAccepted:
					if cdt.Status == apiextensionsv1.ConditionTrue {
						namesAccepted = true
					}
				}
			}
			if established && namesAccepted {
				return v1alpha1.StepStatusCreated, nil
			}
		case v1alpha1.StepStatusUnknown, v1alpha1.StepStatusNotPresent:
			crd, err := crdlib.UnmarshalV1(manifest)
			if err != nil {
				return v1alpha1.StepStatusUnknown, err
			}

			setInstalledAlongsideAnnotation(b.annotator, crd, b.plan.GetNamespace(), step.Resolving, b.csvLister, crd)
			if crd.Labels == nil {
				crd.Labels = map[string]string{}
			}
			crd.Labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue

			_, createError := client.CustomResourceDefinitions().Create(context.TODO(), crd, metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(createError) {
				err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
					currentCRD, _ := client.CustomResourceDefinitions().Get(context.TODO(), crd.GetName(), metav1.GetOptions{})
					crd.SetResourceVersion(currentCRD.GetResourceVersion())
					if err = validateV1CRDCompatibility(b.dynamicClient, currentCRD, crd); err != nil {
						vErr := &validationError{}
						// if the conversion strategy in the new CRD is not "Webhook" OR the error is not a ValidationError
						// return an error. This will catch and return any errors that occur unrelated to actual validation.
						// For example, the API server returning an error when performing a list operation
						if crd.Spec.Conversion == nil || crd.Spec.Conversion.Strategy != apiextensionsv1.WebhookConverter || !errors.As(err, vErr) {
							return fmt.Errorf("error validating existing CRs against new CRD's schema for %q: %w", step.Resource.Name, err)
						}
						// If the conversion strategy in the new CRD is "Webhook" and the error that occurred
						// is an error related to validation, warn that validation failed but that we are trusting
						// that the conversion strategy specified by the author will successfully convert to a format
						// that passes validation and allow the upgrade to continue
						warnTempl := `Validation of existing CRs against the new CRD's schema failed, but a webhook conversion strategy was specified in the new CRD.
The new webhook will only start after the bundle is upgraded, so we must assume that it will successfully convert existing CRs to a format that would have passed validation.

CRD: %q
Validation Error: %s
`
						warnString := fmt.Sprintf(warnTempl, step.Resource.Name, err.Error())
						b.logger.Warn(warnString)
						b.eventRecorder.Event(b.plan, corev1.EventTypeWarning, "CRDValidation", warnString)
					}

					// check to see if stored versions changed and whether the upgrade could cause potential data loss
					safe, err := crdlib.SafeStorageVersionUpgrade(currentCRD, crd)
					if !safe {
						b.logger.Errorf("risk of data loss updating %q: %s", step.Resource.Name, err)
						return fmt.Errorf("risk of data loss updating %q: %w", step.Resource.Name, err)
					}
					if err != nil {
						return fmt.Errorf("checking CRD for potential data loss updating %q: %w", step.Resource.Name, err)
					}

					// Update CRD to new version
					setInstalledAlongsideAnnotation(b.annotator, crd, b.plan.GetNamespace(), step.Resolving, b.csvLister, crd, currentCRD)
					_, err = client.CustomResourceDefinitions().Update(context.TODO(), crd, metav1.UpdateOptions{})
					if err != nil {
						return fmt.Errorf("error updating CRD %q: %w", step.Resource.Name, err)
					}
					return nil
				})
				if err != nil {
					return v1alpha1.StepStatusUnknown, err
				}
				// If it already existed, mark the step as Present.
				// they were equal - mark CRD as present
				return v1alpha1.StepStatusPresent, nil
			} else if createError != nil {
				// Unexpected error creating the CRD.
				return v1alpha1.StepStatusUnknown, createError
			}
			// If no error occurred, make sure to wait for the API to become available.
			return v1alpha1.StepStatusWaitingForAPI, nil
		}
		return v1alpha1.StepStatusUnknown, nil
	}
}

func (b *builder) NewCRDV1Beta1Step(client apiextensionsv1beta1client.ApiextensionsV1beta1Interface, step *v1alpha1.Step, manifest string) StepperFunc {
	return func() (v1alpha1.StepStatus, error) {
		switch step.Status {
		case v1alpha1.StepStatusPresent:
			return v1alpha1.StepStatusPresent, nil
		case v1alpha1.StepStatusCreated:
			return v1alpha1.StepStatusCreated, nil
		case v1alpha1.StepStatusWaitingForAPI:
			crd, err := client.CustomResourceDefinitions().Get(context.TODO(), step.Resource.Name, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return v1alpha1.StepStatusNotPresent, nil
				}
				return v1alpha1.StepStatusNotPresent, fmt.Errorf("error finding the %q CRD: %w", crd.Name, err)
			}
			established, namesAccepted := false, false
			for _, cdt := range crd.Status.Conditions {
				switch cdt.Type {
				case apiextensionsv1beta1.Established:
					if cdt.Status == apiextensionsv1beta1.ConditionTrue {
						established = true
					}
				case apiextensionsv1beta1.NamesAccepted:
					if cdt.Status == apiextensionsv1beta1.ConditionTrue {
						namesAccepted = true
					}
				}
			}
			if established && namesAccepted {
				return v1alpha1.StepStatusCreated, nil
			}
		case v1alpha1.StepStatusUnknown, v1alpha1.StepStatusNotPresent:
			crd, err := crdlib.UnmarshalV1Beta1(manifest)
			if err != nil {
				return v1alpha1.StepStatusUnknown, err
			}

			setInstalledAlongsideAnnotation(b.annotator, crd, b.plan.GetNamespace(), step.Resolving, b.csvLister, crd)
			if crd.Labels == nil {
				crd.Labels = map[string]string{}
			}
			crd.Labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue

			_, createError := client.CustomResourceDefinitions().Create(context.TODO(), crd, metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(createError) {
				err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
					currentCRD, _ := client.CustomResourceDefinitions().Get(context.TODO(), crd.GetName(), metav1.GetOptions{})
					crd.SetResourceVersion(currentCRD.GetResourceVersion())

					if err = validateV1Beta1CRDCompatibility(b.dynamicClient, currentCRD, crd); err != nil {
						return fmt.Errorf("error validating existing CRs against new CRD's schema for %q: %w", step.Resource.Name, err)
					}

					// check to see if stored versions changed and whether the upgrade could cause potential data loss
					safe, err := crdlib.SafeStorageVersionUpgrade(currentCRD, crd)
					if !safe {
						b.logger.Errorf("risk of data loss updating %q: %s", step.Resource.Name, err)
						return fmt.Errorf("risk of data loss updating %q: %w", step.Resource.Name, err)
					}
					if err != nil {
						return fmt.Errorf("checking CRD for potential data loss updating %q: %w", step.Resource.Name, err)
					}

					// Update CRD to new version
					setInstalledAlongsideAnnotation(b.annotator, crd, b.plan.GetNamespace(), step.Resolving, b.csvLister, crd, currentCRD)
					_, err = client.CustomResourceDefinitions().Update(context.TODO(), crd, metav1.UpdateOptions{})
					if err != nil {
						return fmt.Errorf("error updating CRD %q: %w", step.Resource.Name, err)
					}
					return nil
				})
				if err != nil {
					return v1alpha1.StepStatusUnknown, err
				}
				// If it already existed, mark the step as Present.
				// they were equal - mark CRD as present
				return v1alpha1.StepStatusPresent, nil
			} else if createError != nil {
				// Unexpected error creating the CRD.
				return v1alpha1.StepStatusUnknown, createError
			}
			// If no error occurred, make sure to wait for the API to become available.
			return v1alpha1.StepStatusWaitingForAPI, nil
		}
		return v1alpha1.StepStatusUnknown, nil
	}
}

func setInstalledAlongsideAnnotation(a alongside.Annotator, dst metav1.Object, namespace string, name string, lister listersv1alpha1.ClusterServiceVersionLister, srcs ...metav1.Object) {
	var nns []alongside.NamespacedName

	// Only keep references to existing and non-copied CSVs to
	// avoid unbounded growth.
	for _, src := range srcs {
		for _, nn := range a.FromObject(src) {
			if nn.Namespace == namespace && nn.Name == name {
				continue
			}

			if csv, err := lister.ClusterServiceVersions(nn.Namespace).Get(nn.Name); apierrors.IsNotFound(err) {
				continue
			} else if err == nil && csv.IsCopied() {
				continue
			}
			// CSV exists and is not copied OR err is non-nil, but
			// not 404. Safer to assume it exists in unhandled
			// error cases and try again next time.
			nns = append(nns, nn)
		}
	}

	if namespace != "" && name != "" {
		nns = append(nns, alongside.NamespacedName{Namespace: namespace, Name: name})
	}

	a.ToObject(dst, nns)
}

// NewBundleSecretStep returns a StepperFunc for BundleSecret steps (OCPBUGS-35210 Fix 2).
//
// SA-token Secrets must not be created before their owning ServiceAccount exists — the
// Kubernetes token controller (KCM) immediately deletes orphaned token secrets, and
// EnsureBundleSecret would mark the step Created permanently, preventing any retry.
//
// This StepperFunc returns WaitingForAPI when the SA is absent so that NeedsRequeue()
// keeps phase=Installing and OLM retries after 5 s. On the retry the SA has been
// created (it appears later in the plan), and the secret is created successfully.
// WaitingForAPI in the StepperFunc path is handled here directly — it never reaches
// the main ExecutePlan switch that would otherwise skip the step.
func (b *builder) NewBundleSecretStep(step *v1alpha1.Step, manifest string) StepperFunc {
	return func() (v1alpha1.StepStatus, error) {
		switch step.Status {
		case v1alpha1.StepStatusPresent, v1alpha1.StepStatusCreated:
			return step.Status, nil
		}

		namespace := b.plan.GetNamespace()

		var s corev1.Secret
		if err := json.Unmarshal([]byte(manifest), &s); err != nil {
			return v1alpha1.StepStatusUnknown, err
		}

		saName := s.Annotations[corev1.ServiceAccountNameKey]
		if s.Type == corev1.SecretTypeServiceAccountToken && saName != "" {
			_, saErr := b.attenuatedClient.KubernetesInterface().CoreV1().
				ServiceAccounts(namespace).Get(context.TODO(), saName, metav1.GetOptions{})
			if apierrors.IsNotFound(saErr) {
				logrus.WithFields(logrus.Fields{
					"secret": s.Name,
					"sa":     saName,
				}).Info("BundleSecretStep: SA not yet created — returning WaitingForAPI (OCPBUGS-35210)")
				return v1alpha1.StepStatusWaitingForAPI, nil
			}
			// Forbidden means the scoped client lacks get on serviceaccounts; proceed
			// and attempt secret creation — KCM will gate on SA existence regardless.
			if saErr != nil && !apierrors.IsForbidden(saErr) {
				return v1alpha1.StepStatusUnknown, saErr
			}
		}

		s.SetNamespace(namespace)
		if s.Labels == nil {
			s.Labels = map[string]string{}
		}
		s.Labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue

		// Add the resolving CSV as a non-blocking owner so the secret is GC'd on
		// uninstall. Use the lister (catalog-operator credentials, avoids extra API
		// call) — UID is stable once set so a briefly-stale lister entry is safe.
		if step.Resolving != "" {
			csv, err := b.csvLister.ClusterServiceVersions(namespace).Get(step.Resolving)
			if err != nil {
				return v1alpha1.StepStatusUnknown, fmt.Errorf("error getting csv %s for secret owner ref: %w", step.Resolving, err)
			}
			ownerutil.AddNonBlockingOwner(&s, csv)
		}

		// Refresh UIDs on any pre-existing CSV owner refs shipped in the bundle
		// manifest so Kubernetes GC can match them on uninstall.
		updated, err := refreshCSVOwnerRefUIDs(s.OwnerReferences, b.olmClient, namespace)
		if err != nil {
			return v1alpha1.StepStatusUnknown, fmt.Errorf("error refreshing owner references for secret %s: %w", s.GetName(), err)
		}
		s.SetOwnerReferences(updated)

		return createOrUpdateSecret(b.attenuatedClient, namespace, &s)
	}
}

// refreshCSVOwnerRefUIDs populates the UID field on any CSV-kind owner references
// using a live API call, matching the behaviour of getUpdatedOwnerReferences used
// by the old BundleSecret handler. A live call (not the lister) is used so that
// freshly-created CSVs whose UIDs have not yet synced to the informer cache are
// handled correctly.
func refreshCSVOwnerRefUIDs(refs []metav1.OwnerReference, olmClient versioned.Interface, namespace string) ([]metav1.OwnerReference, error) {
	updated := append([]metav1.OwnerReference(nil), refs...)
	for i, owner := range refs {
		if owner.Kind == v1alpha1.ClusterServiceVersionKind {
			csv, err := olmClient.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(context.TODO(), owner.Name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			owner.UID = csv.GetUID()
			updated[i] = owner
		}
	}
	return updated, nil
}

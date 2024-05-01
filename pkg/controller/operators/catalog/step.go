package catalog

import (
	"context"
	"fmt"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
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
	listersv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/internal/alongside"
	crdlib "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/crd"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
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
	dynamicClient    dynamic.Interface
	manifestResolver ManifestResolver
	logger           logrus.FieldLogger
	eventRecorder    record.EventRecorder

	annotator alongside.Annotator
}

func newBuilder(plan *v1alpha1.InstallPlan, csvLister listersv1alpha1.ClusterServiceVersionLister, opclient operatorclient.ClientInterface, dynamicClient dynamic.Interface, manifestResolver ManifestResolver, logger logrus.FieldLogger, er record.EventRecorder) *builder {
	return &builder{
		plan:             plan,
		csvLister:        csvLister,
		opclient:         opclient,
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

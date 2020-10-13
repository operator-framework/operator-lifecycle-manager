package catalog

import (
	"context"
	"fmt"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	crdlib "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/crd"
	index "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/index"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	errorwrap "github.com/pkg/errors"
	logger "github.com/sirupsen/logrus"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextensionsv1client "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apiextensionsv1beta1client "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1beta1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	opclient          operatorclient.ClientInterface
	dynamicClient     dynamic.Interface
	manifestResolver  ManifestResolver
	csvToProvidedAPIs map[string]cache.Indexer
	logger            logger.FieldLogger
}

func newBuilder(opclient operatorclient.ClientInterface, dynamicClient dynamic.Interface, csvToProvidedAPIs map[string]cache.Indexer,
	manifestResolver ManifestResolver, logger logger.FieldLogger) *builder {
	return &builder{
		opclient:          opclient,
		dynamicClient:     dynamicClient,
		manifestResolver:  manifestResolver,
		csvToProvidedAPIs: csvToProvidedAPIs,
		logger:            logger,
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
				if k8serrors.IsNotFound(err) {
					return v1alpha1.StepStatusNotPresent, nil
				} else {
					return v1alpha1.StepStatusNotPresent, errorwrap.Wrapf(err, "error finding the %s CRD", crd.Name)
				}
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

			_, createError := client.CustomResourceDefinitions().Create(context.TODO(), crd, metav1.CreateOptions{})
			if k8serrors.IsAlreadyExists(createError) {
				currentCRD, _ := client.CustomResourceDefinitions().Get(context.TODO(), crd.GetName(), metav1.GetOptions{})
				// Verify CRD ownership, only attempt to update if
				// CRD has only one owner
				// Example: provided=database.coreos.com/v1alpha1/EtcdCluster
				matchedCSV, err := index.V1CRDProviderNames(b.csvToProvidedAPIs, crd)
				if err != nil {
					return v1alpha1.StepStatusUnknown, errorwrap.Wrapf(err, "error find matched CSV: %s", step.Resource.Name)
				}
				crd.SetResourceVersion(currentCRD.GetResourceVersion())
				if len(matchedCSV) == 1 {
					logger.Debugf("Found one owner for CRD %v", crd)
				} else if len(matchedCSV) > 1 {
					logger.Debugf("Found multiple owners for CRD %v", crd)

					if err = validateV1CRDCompatibility(b.dynamicClient, currentCRD, crd); err != nil {
						return v1alpha1.StepStatusUnknown, errorwrap.Wrapf(err, "error validating existing CRs agains new CRD's schema: %s", step.Resource.Name)
					}
				}

				// check to see if stored versions changed and whether the upgrade could cause potential data loss
				safe, err := crdlib.SafeStorageVersionUpgrade(currentCRD, crd)
				if !safe {
					b.logger.Errorf("risk of data loss updating %s: %s", step.Resource.Name, err)
					return v1alpha1.StepStatusUnknown, errorwrap.Wrapf(err, "risk of data loss updating %s", step.Resource.Name)
				}
				if err != nil {
					return v1alpha1.StepStatusUnknown, errorwrap.Wrapf(err, "checking CRD for potential data loss updating %s", step.Resource.Name)
				}

				// Update CRD to new version
				_, err = client.CustomResourceDefinitions().Update(context.TODO(), crd, metav1.UpdateOptions{})
				if err != nil {
					return v1alpha1.StepStatusUnknown, errorwrap.Wrapf(err, "error updating CRD: %s", step.Resource.Name)
				}
				// If it already existed, mark the step as Present.
				// they were equal - mark CRD as present
				return v1alpha1.StepStatusPresent, nil
			} else if createError != nil {
				// Unexpected error creating the CRD.
				return v1alpha1.StepStatusUnknown, createError
			}
			// If no error occured, make sure to wait for the API to become available.
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
				if k8serrors.IsNotFound(err) {
					return v1alpha1.StepStatusNotPresent, nil
				} else {
					return v1alpha1.StepStatusNotPresent, errorwrap.Wrapf(err, "error finding the %s CRD", crd.Name)
				}
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

			_, createError := client.CustomResourceDefinitions().Create(context.TODO(), crd, metav1.CreateOptions{})
			if k8serrors.IsAlreadyExists(createError) {
				currentCRD, _ := client.CustomResourceDefinitions().Get(context.TODO(), crd.GetName(), metav1.GetOptions{})
				// Verify CRD ownership, only attempt to update if
				// CRD has only one owner
				// Example: provided=database.coreos.com/v1alpha1/EtcdCluster
				matchedCSV, err := index.V1Beta1CRDProviderNames(b.csvToProvidedAPIs, crd)
				if err != nil {
					return v1alpha1.StepStatusUnknown, errorwrap.Wrapf(err, "error find matched CSV: %s", step.Resource.Name)
				}
				crd.SetResourceVersion(currentCRD.GetResourceVersion())
				if len(matchedCSV) == 1 {
					logger.Debugf("Found one owner for CRD %v", crd)
				} else if len(matchedCSV) > 1 {
					logger.Debugf("Found multiple owners for CRD %v", crd)

					if err = validateV1Beta1CRDCompatibility(b.dynamicClient, currentCRD, crd); err != nil {
						return v1alpha1.StepStatusUnknown, errorwrap.Wrapf(err, "error validating existing CRs agains new CRD's schema: %s", step.Resource.Name)
					}
				}

				// check to see if stored versions changed and whether the upgrade could cause potential data loss
				safe, err := crdlib.SafeStorageVersionUpgrade(currentCRD, crd)
				if !safe {
					b.logger.Errorf("risk of data loss updating %s: %s", step.Resource.Name, err)
					return v1alpha1.StepStatusUnknown, errorwrap.Wrapf(err, "risk of data loss updating %s", step.Resource.Name)
				}
				if err != nil {
					return v1alpha1.StepStatusUnknown, errorwrap.Wrapf(err, "checking CRD for potential data loss updating %s", step.Resource.Name)
				}

				// Update CRD to new version
				_, err = client.CustomResourceDefinitions().Update(context.TODO(), crd, metav1.UpdateOptions{})
				if err != nil {
					return v1alpha1.StepStatusUnknown, errorwrap.Wrapf(err, "error updating CRD: %s", step.Resource.Name)
				}
				// If it already existed, mark the step as Present.
				// they were equal - mark CRD as present
				return v1alpha1.StepStatusPresent, nil
			} else if createError != nil {
				// Unexpected error creating the CRD.
				return v1alpha1.StepStatusUnknown, createError
			}
			// If no error occured, make sure to wait for the API to become available.
			return v1alpha1.StepStatusWaitingForAPI, nil
		}
		return v1alpha1.StepStatusUnknown, nil
	}
}

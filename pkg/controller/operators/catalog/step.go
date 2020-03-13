package catalog

import (
	"fmt"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	crdlib "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/crd"
	index "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/index"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	errorwrap "github.com/pkg/errors"
	logger "github.com/sirupsen/logrus"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
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
	csvToProvidedAPIs map[string]cache.Indexer
}

func newBuilder(opclient operatorclient.ClientInterface, dynamicClient dynamic.Interface, csvToProvidedAPIs map[string]cache.Indexer) *builder {
	return &builder{
		opclient:          opclient,
		dynamicClient:     dynamicClient,
		csvToProvidedAPIs: csvToProvidedAPIs,
	}
}

type notSupportedStepperErr struct {
	message string
}

func (n notSupportedStepperErr) Error() string {
	return n.message
}

// step is a factory that creates StepperFuncs based on the Kind provided and the install plan step.
func (b *builder) create(step *v1alpha1.Step) (Stepper, error) {
	kind := step.Resource.Kind
	switch kind {
	case crdKind:
		return b.NewCRDStep(step.Resource.Manifest, b.opclient.ApiextensionsInterface(), step.Status, step.Resource.Name), nil
	default:
		return nil, notSupportedStepperErr{fmt.Sprintf("stepper interface does not support %s", kind)}
	}
}

func (b *builder) NewCRDStep(manifest string, client clientset.Interface, status v1alpha1.StepStatus, name string) StepperFunc {
	return func() (v1alpha1.StepStatus, error) {
		switch status {
		case v1alpha1.StepStatusPresent:
			return v1alpha1.StepStatusPresent, nil
		case v1alpha1.StepStatusCreated:
			return v1alpha1.StepStatusCreated, nil
		case v1alpha1.StepStatusWaitingForAPI:
			crd, err := client.ApiextensionsV1().CustomResourceDefinitions().Get(name, metav1.GetOptions{})
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
			crd, err := crdlib.Serialize(manifest)
			if err != nil {
				return v1alpha1.StepStatusUnknown, err
			}

			_, err = client.ApiextensionsV1().CustomResourceDefinitions().Create(crd)
			if k8serrors.IsAlreadyExists(err) {
				currentCRD, _ := client.ApiextensionsV1().CustomResourceDefinitions().Get(crd.GetName(), metav1.GetOptions{})
				// Compare 2 CRDs to see if it needs to be updatetd
				if crdlib.NotEqual(currentCRD, crd) {
					// Verify CRD ownership, only attempt to update if
					// CRD has only one owner
					// Example: provided=database.coreos.com/v1alpha1/EtcdCluster
					matchedCSV, err := index.CRDProviderNames(b.csvToProvidedAPIs, crd)
					if err != nil {
						return v1alpha1.StepStatusUnknown, errorwrap.Wrapf(err, "error find matched CSV: %s", name)
					}
					crd.SetResourceVersion(currentCRD.GetResourceVersion())
					if len(matchedCSV) == 1 {
						logger.Debugf("Found one owner for CRD %v", crd)
					} else if len(matchedCSV) > 1 {
						logger.Debugf("Found multiple owners for CRD %v", crd)

						err := EnsureCRDVersions(currentCRD, crd)
						if err != nil {
							return v1alpha1.StepStatusUnknown, errorwrap.Wrapf(err, "error missing existing CRD version(s) in new CRD: %s", name)
						}

						if err = validateV1CRDCompatibility(b.dynamicClient, currentCRD, crd); err != nil {
							return v1alpha1.StepStatusUnknown, errorwrap.Wrapf(err, "error validating existing CRs agains new CRD's schema: %s", name)
						}
					}
					// Remove deprecated version in CRD storedVersions
					storeVersions := removeDeprecatedV1StoredVersions(currentCRD, crd)
					if storeVersions != nil {
						currentCRD.Status.StoredVersions = storeVersions
						resultCRD, err := client.ApiextensionsV1().CustomResourceDefinitions().UpdateStatus(currentCRD)
						if err != nil {
							return v1alpha1.StepStatusUnknown, errorwrap.Wrapf(err, "error updating CRD's status: %s", name)
						}
						crd.SetResourceVersion(resultCRD.GetResourceVersion())
					}
					// Update CRD to new version
					_, err = client.ApiextensionsV1().CustomResourceDefinitions().Update(crd)
					if err != nil {
						return v1alpha1.StepStatusUnknown, errorwrap.Wrapf(err, "error updating CRD: %s", name)
					}
				}
				// If it already existed, mark the step as Present.
				// they were equal - mark CRD as present
				return v1alpha1.StepStatusPresent, nil
			} else if err != nil {
				// Unexpected error creating the CRD.
				return v1alpha1.StepStatusUnknown, err
			}
			// If no error occured, make sure to wait for the API to become available.
			return v1alpha1.StepStatusWaitingForAPI, nil
		}
		return v1alpha1.StepStatusUnknown, nil
	}
}

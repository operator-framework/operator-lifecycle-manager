package util

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	e2e "k8s.io/kubernetes/test/e2e/framework"

	o "github.com/onsi/gomega"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

type cleanupFunc func()

type csvConditionChecker func(csv *v1alpha1.ClusterServiceVersion) bool

var singleInstance = int32(1)

var CsvFailedChecker = buildCSVConditionChecker(v1alpha1.CSVPhaseFailed)

// This function validates whether a CSV status has a valid phase
func buildCSVConditionChecker(phases ...v1alpha1.ClusterServiceVersionPhase) csvConditionChecker {

	return func(csv *v1alpha1.ClusterServiceVersion) bool {
		conditionMet := false
		for _, phase := range phases {
			conditionMet = conditionMet || csv.Status.Phase == phase
		}
		return conditionMet
	}
}

// This function creates a DeploymentSpec object based off of nginx image
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
						Name:  GenName("nginx"),
						Image: *DummyImage,
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

// This function returns a cleanup function for a CRD that is passed in as an argument
func buildCRDCleanupFunc(c operatorclient.ClientInterface, crdName string) cleanupFunc {
	return func() {
		err := c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Delete(crdName, &metav1.DeleteOptions{GracePeriodSeconds: &immediateDeleteGracePeriod})
		if err != nil {
			e2e.Failf("Failed to delete CRD, error: %v", err)
		}

		_ = waitForDelete(func() error {
			_, err := c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Get(crdName, metav1.GetOptions{})
			return err
		})
	}
}

// This function returns a cleanup function for an API Service that is passed in as an argument
func buildAPIServiceCleanupFunc(c operatorclient.ClientInterface, apiServiceName string) cleanupFunc {
	return func() {
		err := c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Delete(apiServiceName, &metav1.DeleteOptions{GracePeriodSeconds: &immediateDeleteGracePeriod})
		if err != nil {
			e2e.Failf("Failed to delete API service, error: %v", err)
		}

		_ = waitForDelete(func() error {
			_, err := c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Get(apiServiceName, metav1.GetOptions{})
			return err
		})
	}
}

// This function returns a cleanup function for an CSV passed in as an argument
func buildCSVCleanupFunc(c operatorclient.ClientInterface, crc versioned.Interface, csv v1alpha1.ClusterServiceVersion, namespace string, deleteCRDs, deleteAPIServices bool) cleanupFunc {
	return func() {
		err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Delete(csv.GetName(), &metav1.DeleteOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

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
			_, err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(csv.GetName(), metav1.GetOptions{})
			return err
		})
		o.Expect(err).NotTo(o.HaveOccurred())
	}
}

// This function creates a CSV for a given namespace and returns a CSV cleanup function
func CreateCSV(c operatorclient.ClientInterface, crc versioned.Interface, csv v1alpha1.ClusterServiceVersion, namespace string, cleanupCRDs, cleanupAPIServices bool) (cleanupFunc, error) {
	csv.Kind = v1alpha1.ClusterServiceVersionKind
	csv.APIVersion = v1alpha1.SchemeGroupVersion.String()
	_, err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Create(&csv)
	o.Expect(err).NotTo(o.HaveOccurred())
	return buildCSVCleanupFunc(c, crc, csv, namespace, cleanupCRDs, cleanupAPIServices), nil

}

// This function polls and fetches the csv that is passed as an argument
func FetchCSV(c versioned.Interface, csvName, namespace string, checker csvConditionChecker) (*v1alpha1.ClusterServiceVersion, error) {
	var fetched *v1alpha1.ClusterServiceVersion
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, err = c.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(csvName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		e2e.Logf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message)
		return checker(fetched), nil
	})

	if err != nil {
		e2e.Logf("never got correct status: %#v", fetched.Status)
	}
	return fetched, err
}

package e2e

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/runtime_constraints"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8scontrollerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	runtimeConstraintsVolumeMountName = "runtime-constraints"
	runtimeConstraintsConfigMapName   = "runtime-constraints"
	runtimeConstraintsFileName        = "runtime_constraints.json"
	defaultOlmNamespace               = "operator-lifecycle-manager"
	catalogOperatorName               = "catalog-operator"
	catalogContainerIndex             = 0
)

var (
	olmOperatorKey = k8scontrollerclient.ObjectKey{
		Namespace: defaultOlmNamespace,
		Name:      catalogOperatorName,
	}
)

var _ = By

// This suite describes the e2e tests targeting the cluster runtime constraints
// Currently, cluster runtime constraints can be applied to the resolution process
// by including an environment variable (runtime_constraints.RuntimeConstraintEnvVarName)
// that points to the yaml file with the constraints defined.
// The strategy to modify the olm-operator deployment is:
// Before each test:
//  1. Create a new config map in the operator-lifecycle-manager namespace that contains the
//     runtime constraints
//  2. Update the deployment to mount the contents of the config map to /constraints/runtime_constraints.json
//  3. Update the deployment to include the environment variable pointing to the runtime constraints file
//  4. Wait for the deployment to finish updating
//
// After each test:
//   1. Delete the config map
//   2. Revert the changes made to the olm-operator deployment
//   3. Wait for the deployment to finish updating
//
// This process ensures the olm-operator has been started with the runtime constraints as defined in each test
var _ = Describe("Cluster Runtime Constraints", func() {
	var (
		generatedNamespace corev1.Namespace
	)

	BeforeEach(func() {
		generatedNamespace = SetupGeneratedTestNamespace(genName("runtime-constraints-e2e-"))
		setupRuntimeConstraints(ctx.Ctx().Client())
	})

	AfterEach(func() {
		teardownRuntimeConstraints(ctx.Ctx().Client())
		time.Sleep(1 * time.Minute)
		TeardownNamespace(generatedNamespace.GetName())
	})

	It("Runtime", func() {
		time.Sleep(2 * time.Minute)
		require.Equal(GinkgoT(), true, true)
	})
})

func mustDeployRuntimeConstraintsConfigMap(kubeClient k8scontrollerclient.Client) {
	runtimeConstraints := stripMargin(`
		|{
	    |  "properties": [
        |    {
		|      "type": "olm.package",
        |      "value": {
        |        "packageName": "etcd",
        |        "version": "1.0.0"
        |      }
        |    }
        |  ]
        |}`)

	isImmutable := true
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      runtimeConstraintsConfigMapName,
			Namespace: defaultOlmNamespace,
		},
		Immutable: &isImmutable,
		Data: map[string]string{
			runtimeConstraintsFileName: runtimeConstraints,
		},
	}

	err := kubeClient.Create(context.TODO(), configMap)
	if err != nil {
		panic(err)
	}
}

func mustUndeployRuntimeConstraintsConfigMap(kubeClient k8scontrollerclient.Client) {
	if err := kubeClient.Delete(context.TODO(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime-constraints",
			Namespace: "operator-lifecycle-manager",
		},
	}); err != nil {
		panic(err)
	}

	// Wait for config map to be removed
	Eventually(func() bool {
		configMap := &corev1.ConfigMap{}
		err := kubeClient.Get(context.TODO(), k8scontrollerclient.ObjectKey{
			Name:      runtimeConstraintsConfigMapName,
			Namespace: defaultOlmNamespace,
		}, configMap)
		return k8serrors.IsNotFound(err)
	}).Should(BeTrue())
}

func mustPatchCatalogOperatorDeployment(kubeClient k8scontrollerclient.Client) {
	catalogDeployment := &appsv1.Deployment{}
	err := kubeClient.Get(context.TODO(), olmOperatorKey, catalogDeployment)

	if err != nil {
		panic(err)
	}

	volumes := catalogDeployment.Spec.Template.Spec.Volumes
	olmContainer := catalogDeployment.Spec.Template.Spec.Containers[0]

	newVolume := corev1.Volume{
		Name: runtimeConstraintsVolumeMountName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: runtimeConstraintsConfigMapName,
				},
			},
		},
	}

	mountPath := "/constraints"
	newVolumeMount := corev1.VolumeMount{
		Name:      runtimeConstraintsVolumeMountName,
		MountPath: mountPath,
		ReadOnly:  true,
	}

	catalogDeployment.Spec.Template.Spec.Volumes = append(volumes, newVolume)
	catalogDeployment.Spec.Template.Spec.Containers[catalogContainerIndex].VolumeMounts = append(olmContainer.VolumeMounts, newVolumeMount)
	catalogDeployment.Spec.Template.Spec.Containers[catalogContainerIndex].Env = append(olmContainer.Env, corev1.EnvVar{
		Name:  runtime_constraints.RuntimeConstraintEnvVarName,
		Value: fmt.Sprintf("%s/%s", mountPath, runtimeConstraintsFileName),
	})

	err = kubeClient.Update(context.TODO(), catalogDeployment)

	if err != nil {
		panic(err)
	}

	waitForCatalogOperatorDeploymentToUpdate(kubeClient)
}

func mustUnpatchCatalogOperatorDeployment(kubeClient k8scontrollerclient.Client) {
	catalogDeployment := &appsv1.Deployment{}
	err := kubeClient.Get(context.TODO(), olmOperatorKey, catalogDeployment)

	if err != nil {
		panic(err)
	}

	// Remove volume
	volumes := catalogDeployment.Spec.Template.Spec.Volumes
	for index, volume := range volumes {
		if volume.Name == runtimeConstraintsVolumeMountName {
			volumes = append(volumes[:index], volumes[index+1:]...)
			break
		}
	}
	catalogDeployment.Spec.Template.Spec.Volumes = volumes

	// Remove volume mount
	volumeMounts := catalogDeployment.Spec.Template.Spec.Containers[catalogContainerIndex].VolumeMounts
	for index, volumeMount := range volumeMounts {
		if volumeMount.Name == runtimeConstraintsVolumeMountName {
			volumeMounts = append(volumeMounts[:index], volumeMounts[index+1:]...)
		}
	}
	catalogDeployment.Spec.Template.Spec.Containers[catalogContainerIndex].VolumeMounts = volumeMounts

	// Remove environment variable
	envVars := catalogDeployment.Spec.Template.Spec.Containers[catalogContainerIndex].Env
	for index, envVar := range envVars {
		if envVar.Name == runtime_constraints.RuntimeConstraintEnvVarName {
			envVars = append(envVars[:index], envVars[index+1:]...)
		}
	}
	catalogDeployment.Spec.Template.Spec.Containers[catalogContainerIndex].Env = envVars

	err = kubeClient.Update(context.TODO(), catalogDeployment)

	if err != nil {
		panic(err)
	}

	waitForCatalogOperatorDeploymentToUpdate(kubeClient)
}

func setupRuntimeConstraints(kubeClient k8scontrollerclient.Client) {
	mustDeployRuntimeConstraintsConfigMap(kubeClient)
	mustPatchCatalogOperatorDeployment(kubeClient)
}

func teardownRuntimeConstraints(kubeClient k8scontrollerclient.Client) {
	mustUnpatchCatalogOperatorDeployment(kubeClient)
	mustUndeployRuntimeConstraintsConfigMap(kubeClient)
}

func stripMargin(text string) string {
	regex := regexp.MustCompile(`([ \t]+)\|`)
	return strings.TrimSpace(regex.ReplaceAllString(text, ""))
}

// waitForCatalogOperatorDeploymentToUpdate waits for the olm operator deployment to be ready after an update
func waitForCatalogOperatorDeploymentToUpdate(kubeClient k8scontrollerclient.Client) {
	Eventually(func() error {
		deployment := &appsv1.Deployment{}
		err := kubeClient.Get(context.TODO(), olmOperatorKey, deployment)
		if err != nil {
			return err
		}
		// TODO: check that this is the right way to check that a deployment
		//       has finished being updated
		ok := deployment.Status.Replicas == deployment.Status.AvailableReplicas
		if !ok {
			return errors.New("deployment has not yet finished updating")
		}
		return nil
	}).Should(BeNil())
}

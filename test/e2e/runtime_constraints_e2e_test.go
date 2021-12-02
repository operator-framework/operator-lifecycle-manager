package e2e

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

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
	runtimeConstraintsFileName        = "runtime_constraints.yaml"
	defaultOlmNamespace               = "operator-lifecycle-manager"
	olmOperatorName                   = "olm-operator"
	olmContainerIndex                 = 0
)

var (
	olmOperatorKey = k8scontrollerclient.ObjectKey{
		Namespace: defaultOlmNamespace,
		Name:      olmOperatorName,
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
//  2. Update the deployment to mount the contents of the config map to /constraints/runtime_constraints.yaml
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
		TeardownNamespace(generatedNamespace.GetName())
	})

	It("Runtime", func() {
		require.Equal(GinkgoT(), true, true)
	})
})

func mustDeployRuntimeConstraintsConfigMap(kubeClient k8scontrollerclient.Client) {
	runtimeConstraints := stripMargin(`
	    |properties:
	    |  - type: olm.package
        |    value:
        |      packageName: etcd
        |      version: 1.0.0`)

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

func mustPatchOlmDeployment(kubeClient k8scontrollerclient.Client) {
	olmDeployment := &appsv1.Deployment{}
	err := kubeClient.Get(context.TODO(), olmOperatorKey, olmDeployment)

	if err != nil {
		panic(err)
	}

	volumes := olmDeployment.Spec.Template.Spec.Volumes
	olmContainer := olmDeployment.Spec.Template.Spec.Containers[0]

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

	olmDeployment.Spec.Template.Spec.Volumes = append(volumes, newVolume)
	olmDeployment.Spec.Template.Spec.Containers[olmContainerIndex].VolumeMounts = append(olmContainer.VolumeMounts, newVolumeMount)
	olmDeployment.Spec.Template.Spec.Containers[olmContainerIndex].Env = append(olmContainer.Env, corev1.EnvVar{
		Name:  runtime_constraints.RuntimeConstraintEnvVarName,
		Value: fmt.Sprintf("/%s/%s", mountPath, runtimeConstraintsFileName),
	})

	err = kubeClient.Update(context.TODO(), olmDeployment)

	if err != nil {
		panic(err)
	}

	waitForOLMDeploymentToUpdate(kubeClient)
}

func mustUnpatchOlmDeployment(kubeClient k8scontrollerclient.Client) {
	olmDeployment := &appsv1.Deployment{}
	err := kubeClient.Get(context.TODO(), olmOperatorKey, olmDeployment)

	if err != nil {
		panic(err)
	}

	// Remove volume
	volumes := olmDeployment.Spec.Template.Spec.Volumes
	for index, volume := range volumes {
		if volume.Name == runtimeConstraintsVolumeMountName {
			volumes = append(volumes[:index], volumes[index+1:]...)
			break
		}
	}
	olmDeployment.Spec.Template.Spec.Volumes = volumes

	// Remove volume mount
	volumeMounts := olmDeployment.Spec.Template.Spec.Containers[olmContainerIndex].VolumeMounts
	for index, volumeMount := range volumeMounts {
		if volumeMount.Name == runtimeConstraintsVolumeMountName {
			volumeMounts = append(volumeMounts[:index], volumeMounts[index+1:]...)
		}
	}
	olmDeployment.Spec.Template.Spec.Containers[olmContainerIndex].VolumeMounts = volumeMounts

	// Remove environment variable
	envVars := olmDeployment.Spec.Template.Spec.Containers[olmContainerIndex].Env
	for index, envVar := range envVars {
		if envVar.Name == runtime_constraints.RuntimeConstraintEnvVarName {
			envVars = append(envVars[:index], envVars[index+1:]...)
		}
	}

	err = kubeClient.Update(context.TODO(), olmDeployment)

	if err != nil {
		panic(err)
	}

	waitForOLMDeploymentToUpdate(kubeClient)
}

func setupRuntimeConstraints(kubeClient k8scontrollerclient.Client) {
	mustDeployRuntimeConstraintsConfigMap(kubeClient)
	mustPatchOlmDeployment(kubeClient)
}

func teardownRuntimeConstraints(kubeClient k8scontrollerclient.Client) {
	mustUndeployRuntimeConstraintsConfigMap(kubeClient)
	mustUnpatchOlmDeployment(kubeClient)
}

func stripMargin(text string) string {
	regex := regexp.MustCompile(`([ \t]+)\|`)
	return strings.TrimSpace(regex.ReplaceAllString(text, ""))
}

// waitForOLMDeploymentToUpdate waits for the olm operator deployment to be ready after an update
func waitForOLMDeploymentToUpdate(kubeClient k8scontrollerclient.Client) {
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

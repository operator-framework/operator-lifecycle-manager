package utils

import (
	"context"
	"fmt"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"path"
	"time"
)

type Registry struct {
	URL           string
	Auth          string
	Namespace     string
	BundleTool	  string
}

const (
	openshiftregistryFQDN = "image-registry.openshift-image-registry.svc:5000/openshift-operators"
	pollInterval = 1 * time.Second
	pollDuration = 5 * time.Minute
	BuildahTool = "buildah"
	DockerTool = "docker"
)

func initializeRegistry(testNamespace string) (*Registry, func(), error) {
	c := ctx.Ctx().KubeClient()
	local, err := Local(c)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot determine if test running locally or on CI: %s", err)
	}

	var registryURL string
	var registryAuth string
	if local {
		registryURL, err = e2e.CreateDockerRegistry(c, testNamespace)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating container registry: %s", err)
		}

		// ensure registry pod is ready before attempting port-forwarding
		_, err = awaitPod(c, testNamespace, e2e.RegistryName, podReady)
		if err != nil {
			e2e.DeleteDockerRegistry(c, testNamespace)
			return nil, nil, fmt.Errorf("failed to start registry pod: %v", err)
		}

		err = e2e.RegistryPortForward(testNamespace)
		if err != nil {
			e2e.DeleteDockerRegistry(c, testNamespace)
			return nil, nil, fmt.Errorf("port-forwarding local registry: %s", err)
		}
	} else {
		registryURL = openshiftregistryFQDN
		registryAuth, err = e2e.OpenshiftRegistryAuth(c, testNamespace)
		if err != nil {
			return nil, nil, fmt.Errorf("error getting openshift registry authentication: %s", err)
		}
	}
	cleanUpRegistry := func() {
		deleteRegistry(testNamespace)
	}
	return &Registry{URL:registryURL, Auth:registryAuth, Namespace:testNamespace, BundleTool: BuildahTool}, cleanUpRegistry, nil
}

func deleteRegistry(testNamespace string) error {
	c := ctx.Ctx().KubeClient()
	local, err := Local(c)
	if err != nil {
		return fmt.Errorf("cannot determine if test running locally or on CI: %s", err)
	}
	if local {
		e2e.DeleteDockerRegistry(c, testNamespace)
	}
	return nil
}


// podCheckFunc describes a function that returns true if the given Pod meets some criteria; false otherwise.
type podCheckFunc func(pod *corev1.Pod) bool

func awaitPod(c operatorclient.ClientInterface, namespace, name string, checkPod podCheckFunc) (*corev1.Pod, error) {
	var pod *corev1.Pod
	err := wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		p, err := c.KubernetesInterface().CoreV1().Pods(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		pod = p
		return checkPod(pod), nil
	})
	if err != nil {
		return nil, err
	}
	return pod, nil
}

func Local(client operatorclient.ClientInterface) (bool, error) {
	const ClusterVersionGroup = "config.openshift.io"
	const ClusterVersionVersion = "v1"
	const ClusterVersionKind = "ClusterVersion"
	gv := metav1.GroupVersion{Group: ClusterVersionGroup, Version: ClusterVersionVersion}.String()

	groups, err := client.KubernetesInterface().Discovery().ServerResourcesForGroupVersion(gv)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return true, fmt.Errorf("checking if cluster is local: checking server groups: %s", err)
	}

	for _, group := range groups.APIResources {
		if group.Kind == ClusterVersionKind {
			return false, nil
		}
	}

	return true, nil
}

// podReady returns true if the given Pod has a ready condition with ConditionStatus "True"; false otherwise.
func podReady(pod *corev1.Pod) bool {
	var status corev1.ConditionStatus
	for _, condition := range pod.Status.Conditions {
		if condition.Type != corev1.PodReady {
			// Ignore all condition other than PodReady
			continue
		}

		// Found PodReady condition
		status = condition.Status
		break
	}

	return status == corev1.ConditionTrue
}


// Recreates index each time, does not update index. Returns the indexReference to use in CatalogSources
func (r *Registry) CreateBundlesAndIndex(indexName string, bundles []*Bundle) (string, error) {
	bundleRefs := make([]string, 0)

	switch r.BundleTool {
	case DockerTool:
		for _, b := range bundles {
			ref, err := r.buildBundleImage(b)
			if err != nil {
				return "", err
			}
			bundleRefs = append(bundleRefs, ref)
		}

		if err := r.uploadBundleReferences(bundleRefs); err != nil {
			return "", err
		}
	case BuildahTool:
		for _, b := range bundles {
			labels := generateBundleLabels(b.PackageName, b.DefaultChannel, b.Channels)
			imageRef, err := createLocalBundleImage(path.Dir(b.BundleManifestDirectory), b.BundlePath, []string{"manifests", "metadata"}, labels)
			if err != nil {
				return "", err
			}
			if err := r.skopeoCopy(fmt.Sprintf("%s/%s:", r.URL, b.BundlePath), b.Version, imageRef, ""); err != nil {
				return "", err
			}
			bundleRefs = append(bundleRefs, imageRef)
		}
	}

	indexReference, err := r.CreateAndUploadIndex(indexName, bundleRefs)
	if err != nil {
		return "", err
	}
	return indexReference, nil
}
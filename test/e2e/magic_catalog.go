package e2e

import (
	"context"
	"fmt"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	k8serror "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8scontrollerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	olmCatalogLabel    string = "olm.catalogSource"
	catalogMountPath   string = "/opt/olm"
	catalogServicePort int32  = 50051
	catalogReadyState  string = "READY"
)

type MagicCatalog interface {
	DeployCatalog(ctx context.Context) error
	UndeployCatalog(ctx context.Context) []error
}

type magicCatalog struct {
	fileBasedCatalog FileBasedCatalogProvider
	kubeClient       k8scontrollerclient.Client
	namespace        string
	name             string
	configMapName    string
	serviceName      string
	podName          string
}

// NewMagicCatalog creates an object that can deploy an arbitrary file-based catalog given by the FileBasedCatalogProvider
// Keep in mind that there are limits to the configMaps. So, the catalogs need to be relatively simple
func NewMagicCatalog(kubeClient k8scontrollerclient.Client, namespace string, catalogName string, provider FileBasedCatalogProvider) MagicCatalog {
	return &magicCatalog{
		fileBasedCatalog: provider,
		kubeClient:       kubeClient,
		namespace:        namespace,
		name:             catalogName,
		configMapName:    catalogName + "-configmap",
		serviceName:      catalogName + "-svc",
		podName:          catalogName + "-pod",
	}
}

func (c *magicCatalog) DeployCatalog(ctx context.Context) error {

	catalogSource := c.makeCatalogSource()
	resourcesInOrderOfDeployment := []k8scontrollerclient.Object{
		c.makeConfigMap(),
		c.makeCatalogSourcePod(),
		c.makeCatalogService(),
		catalogSource,
	}

	for _, res := range resourcesInOrderOfDeployment {
		err := c.kubeClient.Create(ctx, res)
		if err != nil {
			return c.cleanUpAfter(ctx, err)
		}
	}

	// wait for catalog source to become ready
	err := waitFor(func() (bool, error) {
		err := c.kubeClient.Get(ctx, k8scontrollerclient.ObjectKey{
			Name:      catalogSource.GetName(),
			Namespace: catalogSource.GetNamespace(),
		}, catalogSource)

		if err != nil || catalogSource.Status.GRPCConnectionState == nil {
			return false, err
		}

		state := catalogSource.Status.GRPCConnectionState.LastObservedState

		if state != catalogReadyState {
			return false, nil
		} else {
			return true, nil
		}
	})

	if err != nil {
		return c.cleanUpAfter(ctx, err)
	}

	return nil
}

func (c *magicCatalog) UndeployCatalog(ctx context.Context) []error {
	var errs []error = nil

	resourcesInOrderOfDeletion := []k8scontrollerclient.Object{
		c.makeCatalogSource(),
		c.makeCatalogService(),
		c.makeCatalogSourcePod(),
		c.makeConfigMap(),
	}

	// try to delete all resourcesInOrderOfDeletion even if errors are
	// encountered through deletion.
	for _, res := range resourcesInOrderOfDeletion {
		err := c.kubeClient.Delete(ctx, res)

		// ignore not found errors
		if err != nil && !k8serror.IsNotFound(err) {
			if errs == nil {
				errs = make([]error, 0)
			}
			errs = append(errs, err)
		}
	}

	return errs
}

func (c *magicCatalog) cleanUpAfter(ctx context.Context, err error) error {
	cleanupErr := c.UndeployCatalog(ctx)
	if cleanupErr != nil {
		return fmt.Errorf("the following cleanup errors occurred: '%s' after an error deploying the configmap: '%s' ", cleanupErr, err)
	}
	return err
}

func (c *magicCatalog) makeCatalogService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.serviceName,
			Namespace: c.namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "grpc",
					Port:       catalogServicePort,
					Protocol:   "TCP",
					TargetPort: intstr.FromInt(int(catalogServicePort)),
				},
			},
			Selector: c.makeCatalogSourcePodLabels(),
		},
	}
}

func (c *magicCatalog) makeConfigMap() *corev1.ConfigMap {
	isImmutable := true
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.configMapName,
			Namespace: c.namespace,
		},
		Immutable: &isImmutable,
		Data: map[string]string{
			"catalog.json": c.fileBasedCatalog.GetCatalog(),
			// due to the way files get mounted to pods from configMaps
			// it is important to add _this_ .indexignore
			//
			// The mount folder will look something like this:
			// /opt/olm
			// |--> ..2021_12_15_02_01_11.729011450
			//      |--> catalog.json
			//      |--> .indexignore
			// |--> ..data -> ..2021_12_15_02_01_11.729011450
			// |--> catalog.json -> ..data/catalog.json
			// |--> .indexignore -> ..data/.indexignore
			// Adding '**/..*' to the .indexignore ensures the
			// '..2021_12_15_02_01_11.729011450' and ' ..data' directories are ignored.
			// Otherwise, opm will pick up on both catalog.json files and fail with a conflicts (duplicate packages)
			".indexignore": "**/\\.\\.*\n",
		},
	}
}

func (c *magicCatalog) makeCatalogSource() *operatorsv1alpha1.CatalogSource {
	return &operatorsv1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.name,
			Namespace: c.namespace,
		},
		Spec: operatorsv1alpha1.CatalogSourceSpec{
			SourceType: operatorsv1alpha1.SourceTypeGrpc,
			Address:    fmt.Sprintf("%s.%s.svc:50051", c.serviceName, c.namespace),
		},
	}
}

func (c *magicCatalog) makeCatalogSourcePod() *corev1.Pod {

	const (
		image                  = "quay.io/operator-framework/upstream-opm-builder"
		readinessDelay  int32  = 5
		livenessDelay   int32  = 10
		volumeMountName string = "fbc-catalog"
	)

	readOnlyRootFilesystem := false

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.podName,
			Namespace: c.namespace,
			Labels:    c.makeCatalogSourcePodLabels(),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "catalog",
					Image:   image,
					Command: []string{"opm", "serve", catalogMountPath},
					Ports: []corev1.ContainerPort{
						{
							Name:          "grpc",
							ContainerPort: 50051,
						},
					},
					ReadinessProbe: &corev1.Probe{
						Handler: corev1.Handler{
							Exec: &corev1.ExecAction{
								Command: []string{"grpc_health_probe", "-addr=:50051"},
							},
						},
						InitialDelaySeconds: readinessDelay,
						TimeoutSeconds:      5,
					},
					LivenessProbe: &corev1.Probe{
						Handler: corev1.Handler{
							Exec: &corev1.ExecAction{
								Command: []string{"grpc_health_probe", "-addr=:50051"},
							},
						},
						InitialDelaySeconds: livenessDelay,
						TimeoutSeconds:      5,
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("10m"),
							corev1.ResourceMemory: resource.MustParse("50Mi"),
						},
					},
					SecurityContext: &corev1.SecurityContext{
						ReadOnlyRootFilesystem: &readOnlyRootFilesystem,
					},
					ImagePullPolicy:          corev1.PullAlways,
					TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      volumeMountName,
							MountPath: catalogMountPath,
							ReadOnly:  true,
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: volumeMountName,
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: c.configMapName,
							},
						},
					},
				},
			},
		},
	}
}

func (c *magicCatalog) makeCatalogSourcePodLabels() map[string]string {
	return map[string]string{
		olmCatalogLabel: c.name,
	}
}

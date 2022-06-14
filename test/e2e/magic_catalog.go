package e2e

import (
	"context"
	"fmt"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	k8serror "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8scontrollerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	olmCatalogLabel    string = "olm.catalogSource"
	catalogMountPath   string = "/opt/olm"
	catalogServicePort int32  = 50051
	catalogReadyState  string = "READY"
)

type MagicCatalog struct {
	fileBasedCatalog FileBasedCatalogProvider
	kubeClient       k8scontrollerclient.Client
	name             string
	namespace        string
	configMapName    string
	serviceName      string
	podName          string
}

// NewMagicCatalog creates an object that can deploy an arbitrary file-based catalog given by the FileBasedCatalogProvider
// Keep in mind that there are limits to the configMaps. So, the catalogs need to be relatively simple
func NewMagicCatalog(kubeClient k8scontrollerclient.Client, namespace string, catalogName string, provider FileBasedCatalogProvider) *MagicCatalog {
	return &MagicCatalog{
		fileBasedCatalog: provider,
		kubeClient:       kubeClient,
		namespace:        namespace,
		name:             catalogName,
		configMapName:    catalogName + "-configmap",
		serviceName:      catalogName + "-svc",
		podName:          catalogName + "-pod",
	}
}

func NewMagicCatalogFromFile(kubeClient k8scontrollerclient.Client, namespace string, catalogName string, fbcFilePath string) (*MagicCatalog, error) {
	provider, err := NewFileBasedFiledBasedCatalogProvider(fbcFilePath)
	if err != nil {
		return nil, err
	}
	catalog := NewMagicCatalog(kubeClient, namespace, catalogName, provider)
	return catalog, nil
}

func (c *MagicCatalog) GetName() string {
	return c.name
}

func (c *MagicCatalog) GetNamespace() string {
	return c.namespace
}

func (c *MagicCatalog) DeployCatalog(ctx context.Context) error {
	resourcesInOrderOfDeployment := []k8scontrollerclient.Object{
		c.makeConfigMap(),
		c.makeCatalogSourcePod(),
		c.makeCatalogService(),
		c.makeCatalogSource(),
	}
	if err := c.deployCatalog(ctx, resourcesInOrderOfDeployment); err != nil {
		return err
	}
	if err := c.catalogSourceIsReady(ctx); err != nil {
		return c.cleanUpAfterError(ctx, err)
	}

	return nil
}

func (c *MagicCatalog) UpdateCatalog(ctx context.Context, provider FileBasedCatalogProvider) error {
	resourcesInOrderOfDeletion := []k8scontrollerclient.Object{
		c.makeCatalogSourcePod(),
		c.makeConfigMap(),
	}
	errors := c.undeployCatalog(ctx, resourcesInOrderOfDeletion)
	if len(errors) != 0 {
		return utilerrors.NewAggregate(errors)
	}

	// TODO(tflannag): Create a pod watcher struct and setup an underlying watch
	// and block until ctx.Done()?
	err := waitFor(func() (bool, error) {
		pod := &corev1.Pod{}
		err := c.kubeClient.Get(ctx, k8scontrollerclient.ObjectKey{
			Name:      c.podName,
			Namespace: c.namespace,
		}, pod)
		if k8serror.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
	if err != nil {
		return fmt.Errorf("failed to successfully update the catalog deployment: %v", err)
	}

	c.fileBasedCatalog = provider
	resourcesInOrderOfCreation := []k8scontrollerclient.Object{
		c.makeConfigMap(),
		c.makeCatalogSourcePod(),
	}
	if err := c.deployCatalog(ctx, resourcesInOrderOfCreation); err != nil {
		return err
	}
	if err := c.catalogSourceIsReady(ctx); err != nil {
		return c.cleanUpAfterError(ctx, err)
	}

	return nil
}

func (c *MagicCatalog) UndeployCatalog(ctx context.Context) []error {
	resourcesInOrderOfDeletion := []k8scontrollerclient.Object{
		c.makeCatalogSource(),
		c.makeCatalogService(),
		c.makeCatalogSourcePod(),
		c.makeConfigMap(),
	}
	return c.undeployCatalog(ctx, resourcesInOrderOfDeletion)
}

func (c *MagicCatalog) catalogSourceIsReady(ctx context.Context) error {
	// wait for catalog source to become ready
	key := k8scontrollerclient.ObjectKey{
		Name:      c.name,
		Namespace: c.namespace,
	}

	return waitFor(func() (bool, error) {
		catalogSource := &operatorsv1alpha1.CatalogSource{}
		err := c.kubeClient.Get(ctx, key, catalogSource)
		if err != nil || catalogSource.Status.GRPCConnectionState == nil {
			return false, err
		}
		state := catalogSource.Status.GRPCConnectionState.LastObservedState
		if state != catalogReadyState {
			return false, nil
		}
		return true, nil
	})
}

func (c *MagicCatalog) deployCatalog(ctx context.Context, resources []k8scontrollerclient.Object) error {
	for _, res := range resources {
		err := c.kubeClient.Create(ctx, res)
		if err != nil {
			return c.cleanUpAfterError(ctx, err)
		}
	}
	return nil
}

func (c *MagicCatalog) undeployCatalog(ctx context.Context, resources []k8scontrollerclient.Object) []error {
	var errors []error
	// try to delete all resourcesInOrderOfDeletion even if errors are
	// encountered through deletion.
	for _, res := range resources {
		err := c.kubeClient.Delete(ctx, res)

		// ignore not found errors
		if err != nil && !k8serror.IsNotFound(err) {
			if errors == nil {
				errors = make([]error, 0)
			}
			errors = append(errors, err)
		}
	}
	return errors
}

func (c *MagicCatalog) cleanUpAfterError(ctx context.Context, err error) error {
	cleanupErr := c.UndeployCatalog(ctx)
	if cleanupErr != nil {
		return fmt.Errorf("the following cleanup errors occurred: '%s' after an error deploying the configmap: '%s' ", cleanupErr, err)
	}
	return err
}

func (c *MagicCatalog) makeCatalogService() *corev1.Service {
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

func (c *MagicCatalog) makeConfigMap() *corev1.ConfigMap {
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

func (c *MagicCatalog) makeCatalogSource() *operatorsv1alpha1.CatalogSource {
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

func (c *MagicCatalog) makeCatalogSourcePod() *corev1.Pod {

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
						ProbeHandler: corev1.ProbeHandler{
							Exec: &corev1.ExecAction{
								Command: []string{"grpc_health_probe", "-addr=:50051"},
							},
						},
						InitialDelaySeconds: readinessDelay,
						TimeoutSeconds:      5,
					},
					LivenessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
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

func (c *MagicCatalog) makeCatalogSourcePodLabels() map[string]string {
	return map[string]string{
		olmCatalogLabel: c.name,
	}
}

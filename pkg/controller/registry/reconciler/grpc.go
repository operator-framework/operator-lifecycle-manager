package reconciler

import (
	"context"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	k8serror "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	controllerclient "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/controller-runtime/client"
	hashutil "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubernetes/pkg/util/hash"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

const (
	CatalogSourceUpdateKey      = "catalogsource.operators.coreos.com/update"
	ServiceHashLabelKey         = "olm.service-spec-hash"
	CatalogPollingRequeuePeriod = 30 * time.Second
)

// grpcCatalogSourceDecorator wraps CatalogSource to add additional methods
type grpcCatalogSourceDecorator struct {
	*v1alpha1.CatalogSource
}

type UpdateNotReadyErr struct {
	catalogName string
	podName     string
}

func (u UpdateNotReadyErr) Error() string {
	return fmt.Sprintf("catalog polling: %s not ready for update: update pod %s has not yet reported ready", u.catalogName, u.podName)
}

func (s *grpcCatalogSourceDecorator) Selector() labels.Selector {
	return labels.SelectorFromValidatedSet(map[string]string{
		CatalogSourceLabelKey: s.GetName(),
	})
}

func (s *grpcCatalogSourceDecorator) SelectorForUpdate() labels.Selector {
	return labels.SelectorFromValidatedSet(map[string]string{
		CatalogSourceUpdateKey: s.GetName(),
	})
}

func (s *grpcCatalogSourceDecorator) Labels() map[string]string {
	return map[string]string{
		CatalogSourceLabelKey: s.GetName(),
	}
}

func (s *grpcCatalogSourceDecorator) Annotations() map[string]string {
	// TODO: Maybe something better than just a copy of all annotations would be to have a specific 'podMetadata' section in the CatalogSource?
	return s.GetAnnotations()
}

func (s *grpcCatalogSourceDecorator) Service() *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.GetName(),
			Namespace: s.GetNamespace(),
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "grpc",
					Port:       50051,
					TargetPort: intstr.FromInt(50051),
				},
			},
			Selector: s.Labels(),
		},
	}

	labels := map[string]string{}
	hash := HashServiceSpec(svc.Spec)
	labels[ServiceHashLabelKey] = hash
	svc.SetLabels(labels)
	ownerutil.AddOwner(svc, s.CatalogSource, false, false)
	return svc
}

func (s *grpcCatalogSourceDecorator) ServiceAccount() *corev1.ServiceAccount {
	var secrets []corev1.LocalObjectReference
	blockOwnerDeletion := true
	isController := true
	for _, secretName := range s.CatalogSource.Spec.Secrets {
		if secretName == "" {
			continue
		}
		secrets = append(secrets, corev1.LocalObjectReference{Name: secretName})
	}
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.GetName(),
			Namespace: s.GetNamespace(),
			OwnerReferences: []metav1.OwnerReference{
				{
					Name:               s.GetName(),
					Kind:               v1alpha1.CatalogSourceKind,
					APIVersion:         v1alpha1.CatalogSourceCRDAPIVersion,
					UID:                s.GetUID(),
					Controller:         &isController,
					BlockOwnerDeletion: &blockOwnerDeletion,
				},
			},
		},
		ImagePullSecrets: secrets,
	}
}

func (s *grpcCatalogSourceDecorator) Pod(saName string) *corev1.Pod {
	pod := Pod(s.CatalogSource, "registry-server", s.Spec.Image, saName, s.Labels(), s.Annotations(), 5, 10)
	ownerutil.AddOwner(pod, s.CatalogSource, false, false)
	return pod
}

type GrpcRegistryReconciler struct {
	now       nowFunc
	Lister    operatorlister.OperatorLister
	OpClient  operatorclient.ClientInterface
	SSAClient *controllerclient.ServerSideApplier
}

var _ RegistryReconciler = &GrpcRegistryReconciler{}

func (c *GrpcRegistryReconciler) currentService(source grpcCatalogSourceDecorator) *corev1.Service {
	serviceName := source.Service().GetName()
	service, err := c.Lister.CoreV1().ServiceLister().Services(source.GetNamespace()).Get(serviceName)
	if err != nil {
		logrus.WithField("service", serviceName).Warn("couldn't find service in cache")
		return nil
	}
	return service
}

func (c *GrpcRegistryReconciler) currentPods(source grpcCatalogSourceDecorator) []*corev1.Pod {
	pods, err := c.Lister.CoreV1().PodLister().Pods(source.GetNamespace()).List(source.Selector())
	if err != nil {
		logrus.WithError(err).Warn("couldn't find pod in cache")
		return nil
	}
	if len(pods) > 1 {
		logrus.WithField("selector", source.Selector()).Warn("multiple pods found for selector")
	}
	return pods
}

func (c *GrpcRegistryReconciler) currentUpdatePods(source grpcCatalogSourceDecorator) []*corev1.Pod {
	pods, err := c.Lister.CoreV1().PodLister().Pods(source.GetNamespace()).List(source.SelectorForUpdate())
	if err != nil {
		logrus.WithError(err).Warn("couldn't find pod in cache")
		return nil
	}
	if len(pods) > 1 {
		logrus.WithField("selector", source.Selector()).Warn("multiple pods found for selector")
	}
	return pods
}

func (c *GrpcRegistryReconciler) currentPodsWithCorrectImage(source grpcCatalogSourceDecorator) []*corev1.Pod {
	pods, err := c.Lister.CoreV1().PodLister().Pods(source.GetNamespace()).List(labels.SelectorFromValidatedSet(source.Labels()))
	if err != nil {
		logrus.WithError(err).Warn("couldn't find pod in cache")
		return nil
	}
	found := []*corev1.Pod{}
	for _, p := range pods {
		if p.Spec.Containers[0].Image == source.Spec.Image {
			found = append(found, p)
		}
	}
	return found
}

// EnsureRegistryServer ensures that all components of registry server are up to date.
func (c *GrpcRegistryReconciler) EnsureRegistryServer(catalogSource *v1alpha1.CatalogSource) error {
	source := grpcCatalogSourceDecorator{catalogSource}

	// if service status is nil, we force create every object to ensure they're created the first time
	overwrite := source.Status.RegistryServiceStatus == nil
	// recreate the pod if no existing pod is serving the latest image
	overwritePod := overwrite || len(c.currentPodsWithCorrectImage(source)) == 0

	//TODO: if any of these error out, we should write a status back (possibly set RegistryServiceStatus to nil so they get recreated)
	sa, err := c.ensureSA(source)
	if err != nil && !k8serror.IsAlreadyExists(err) {
		return errors.Wrapf(err, "error ensuring service account: %s", source.GetName())
	}
	if err := c.ensurePod(source, sa.GetName(), overwritePod); err != nil {
		return errors.Wrapf(err, "error ensuring pod: %s", source.Pod(sa.Name).GetName())
	}
	if err := c.ensureUpdatePod(source, sa.Name); err != nil {
		if _, ok := err.(UpdateNotReadyErr); ok {
			return err
		}
		return errors.Wrapf(err, "error ensuring updated catalog source pod: %s", source.Pod(sa.Name).GetName())
	}
	if err := c.ensureService(source, overwrite); err != nil {
		return errors.Wrapf(err, "error ensuring service: %s", source.Service().GetName())
	}

	if overwritePod {
		now := c.now()
		catalogSource.Status.RegistryServiceStatus = &v1alpha1.RegistryServiceStatus{
			CreatedAt:        now,
			Protocol:         "grpc",
			ServiceName:      source.Service().GetName(),
			ServiceNamespace: source.GetNamespace(),
			Port:             fmt.Sprintf("%d", source.Service().Spec.Ports[0].Port),
		}
	}
	return nil
}

func (c *GrpcRegistryReconciler) ensurePod(source grpcCatalogSourceDecorator, saName string, overwrite bool) error {
	// currentLivePods refers to the currently live instances of the catalog source
	currentLivePods := c.currentPods(source)
	if len(currentLivePods) > 0 {
		if !overwrite {
			return nil
		}
		for _, p := range currentLivePods {
			if err := c.OpClient.KubernetesInterface().CoreV1().Pods(source.GetNamespace()).Delete(context.TODO(), p.GetName(), *metav1.NewDeleteOptions(1)); err != nil {
				return errors.Wrapf(err, "error deleting old pod: %s", p.GetName())
			}
		}
	}
	_, err := c.OpClient.KubernetesInterface().CoreV1().Pods(source.GetNamespace()).Create(context.TODO(), source.Pod(saName), metav1.CreateOptions{})
	if err != nil {
		return errors.Wrapf(err, "error creating new pod: %s", source.Pod(saName).GetGenerateName())
	}

	return nil
}

// ensureUpdatePod checks that for the same catalog source version the same container imageID is running
func (c *GrpcRegistryReconciler) ensureUpdatePod(source grpcCatalogSourceDecorator, saName string) error {
	if !source.Poll() {
		return nil
	}

	currentLivePods := c.currentPods(source)
	currentUpdatePods := c.currentUpdatePods(source)

	if source.Update() && len(currentUpdatePods) == 0 {
		logrus.WithField("CatalogSource", source.GetName()).Infof("catalog update required at %s", time.Now().String())
		pod, err := c.createUpdatePod(source, saName)
		if err != nil {
			return errors.Wrapf(err, "creating update catalog source pod")
		}
		source.SetLastUpdateTime()
		return UpdateNotReadyErr{catalogName: source.GetName(), podName: pod.GetName()}
	}

	// check if update pod is ready - if not requeue the sync
	// if update pod failed (potentially due to a bad catalog image) delete it
	for _, p := range currentUpdatePods {
		fail, err := c.podFailed(p)
		if err != nil {
			return err
		}
		if fail {
			return fmt.Errorf("update pod %s in a %s state: deleted update pod", p.GetName(), p.Status.Phase)
		}
		if !podReady(p) {
			return UpdateNotReadyErr{catalogName: source.GetName(), podName: p.GetName()}
		}
	}

	for _, updatePod := range currentUpdatePods {
		// if container imageID IDs are different, switch the serving pods
		if imageChanged(updatePod, currentLivePods) {
			err := c.promoteCatalog(updatePod, source.GetName())
			if err != nil {
				return fmt.Errorf("detected imageID change: error during update: %s", err)
			}
			// remove old catalog source pod
			err = c.removePods(currentLivePods, source.GetNamespace())
			if err != nil {
				return errors.Wrapf(err, "detected imageID change: error deleting old catalog source pod")
			}
			// done syncing
			logrus.WithField("CatalogSource", source.GetName()).Infof("detected imageID change: catalogsource pod updated at %s", time.Now().String())
			return nil
		}
		// delete update pod right away, since the digest match, to prevent long-lived duplicate catalog pods
		logrus.WithField("CatalogSource", source.GetName()).Info("catalog polling result: no update")
		err := c.removePods([]*corev1.Pod{updatePod}, source.GetNamespace())
		if err != nil {
			return errors.Wrapf(err, "error deleting duplicate catalog polling pod: %s", updatePod.GetName())
		}
	}

	return nil
}

func (c *GrpcRegistryReconciler) ensureService(source grpcCatalogSourceDecorator, overwrite bool) error {
	service := source.Service()
	svc := c.currentService(source)
	if svc != nil {
		if !overwrite && ServiceHashMatch(svc, service) {
			return nil
		}
		// TODO(tflannag): Do we care about force deleting services?
		if err := c.OpClient.DeleteService(service.GetNamespace(), service.GetName(), metav1.NewDeleteOptions(0)); err != nil {
			return err
		}
	}
	_, err := c.OpClient.CreateService(service)
	return err
}

func (c *GrpcRegistryReconciler) ensureSA(source grpcCatalogSourceDecorator) (*v1.ServiceAccount, error) {
	sa := source.ServiceAccount()
	if _, err := c.OpClient.CreateServiceAccount(sa); err != nil {
		return sa, err
	}
	return sa, nil
}

// ServiceHashMatch will check the hash info in existing Service to ensure its
// hash info matches the desired Service's hash.
func ServiceHashMatch(existing, new *corev1.Service) bool {
	labels := existing.GetLabels()
	newLabels := new.GetLabels()
	if len(labels) == 0 || len(newLabels) == 0 {
		return false
	}

	existingSvcSpecHash, ok := labels[ServiceHashLabelKey]
	if !ok {
		return false
	}

	newSvcSpecHash, ok := newLabels[ServiceHashLabelKey]
	if !ok {
		return false
	}

	if existingSvcSpecHash != newSvcSpecHash {
		return false
	}

	return true
}

// HashServiceSpec calculates a hash given a copy of the service spec
func HashServiceSpec(spec corev1.ServiceSpec) string {
	hasher := fnv.New32a()
	hashutil.DeepHashObject(hasher, &spec)
	return rand.SafeEncodeString(fmt.Sprint(hasher.Sum32()))
}

// createUpdatePod is an internal method that creates a pod using the latest catalog source.
func (c *GrpcRegistryReconciler) createUpdatePod(source grpcCatalogSourceDecorator, saName string) (*corev1.Pod, error) {
	// remove label from pod to ensure service does not accidentally route traffic to the pod
	p := source.Pod(saName)
	p = swapLabels(p, "", source.Name)

	pod, err := c.OpClient.KubernetesInterface().CoreV1().Pods(source.GetNamespace()).Create(context.TODO(), p, metav1.CreateOptions{})
	if err != nil {
		logrus.WithField("pod", source.Pod(saName).GetName()).Warn("couldn't create new catalogsource pod")
		return nil, err
	}

	return pod, nil
}

// checkUpdatePodDigest checks update pod to get Image ID and see if it matches the serving (live) pod ImageID
func imageChanged(updatePod *corev1.Pod, servingPods []*corev1.Pod) bool {
	updatedCatalogSourcePodImageID := imageID(updatePod)
	if updatedCatalogSourcePodImageID == "" {
		logrus.WithField("CatalogSource", updatePod.GetName()).Warn("pod status unknown, cannot get the pod's imageID")
		return false
	}
	for _, servingPod := range servingPods {
		servingCatalogSourcePodImageID := imageID(servingPod)
		if updatedCatalogSourcePodImageID != servingCatalogSourcePodImageID {
			logrus.WithField("CatalogSource", servingPod.GetName()).Infof("catalog image changed: serving pod %s update pod %s", servingCatalogSourcePodImageID, updatedCatalogSourcePodImageID)
			return true
		}
	}

	return false
}

// imageID returns the ImageID of the primary catalog source container or an empty string if the image ID isn't available yet.
// Note: the pod must be running and the container in a ready status to return a valid ImageID.
func imageID(pod *corev1.Pod) string {
	if len(pod.Status.ContainerStatuses) < 1 {
		logrus.WithField("CatalogSource", pod.GetName()).Warn("pod status unknown")
		return ""
	}

	return pod.Status.ContainerStatuses[0].ImageID
}

func (c *GrpcRegistryReconciler) removePods(pods []*corev1.Pod, namespace string) error {
	for _, p := range pods {
		err := c.OpClient.KubernetesInterface().CoreV1().Pods(namespace).Delete(context.TODO(), p.GetName(), *metav1.NewDeleteOptions(1))
		if err != nil {
			return errors.Wrapf(err, "error deleting pod: %s", p.GetName())
		}
	}
	return nil
}

// CheckRegistryServer returns true if the given CatalogSource is considered healthy; false otherwise.
func (c *GrpcRegistryReconciler) CheckRegistryServer(catalogSource *v1alpha1.CatalogSource) (healthy bool, err error) {
	source := grpcCatalogSourceDecorator{catalogSource}

	// Check on registry resources
	// TODO: add gRPC health check
	if len(c.currentPodsWithCorrectImage(source)) < 1 ||
		c.currentService(source) == nil {
		healthy = false
		return
	}

	healthy = true
	return
}

// promoteCatalog swaps the labels on the update pod so that the update pod is now reachable by the catalog service.
// By updating the catalog on cluster it promotes the update pod to act as the new version of the catalog on-cluster.
func (c *GrpcRegistryReconciler) promoteCatalog(updatePod *corev1.Pod, key string) error {
	// Update the update pod to promote it to serving pod via the SSA client
	err := c.SSAClient.Apply(context.TODO(), updatePod, func(p *corev1.Pod) error {
		p.Labels[CatalogSourceLabelKey] = key
		p.Labels[CatalogSourceUpdateKey] = ""
		return nil
	})()

	return err
}

// podReady returns true if the given Pod has a ready status condition.
func podReady(pod *corev1.Pod) bool {
	if pod.Status.Conditions == nil {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func swapLabels(pod *corev1.Pod, labelKey, updateKey string) *corev1.Pod {
	pod.Labels[CatalogSourceLabelKey] = labelKey
	pod.Labels[CatalogSourceUpdateKey] = updateKey
	return pod
}

// podFailed checks whether the pod status is in a failed or unknown state, and deletes the pod if so.
func (c *GrpcRegistryReconciler) podFailed(pod *corev1.Pod) (bool, error) {
	if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodUnknown {
		logrus.WithField("UpdatePod", pod.GetName()).Infof("catalog polling result: update pod %s failed to start", pod.GetName())
		err := c.removePods([]*corev1.Pod{pod}, pod.GetNamespace())
		if err != nil {
			return true, errors.Wrapf(err, "error deleting failed catalog polling pod: %s", pod.GetName())
		}
		return true, nil
	}
	return false, nil
}

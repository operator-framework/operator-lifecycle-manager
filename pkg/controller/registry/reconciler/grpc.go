package reconciler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	pkgerrors "github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
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
	createPodAsUser int64
	opmImage        string
	utilImage       string
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
		CatalogSourceLabelKey:      s.GetName(),
		install.OLMManagedLabelKey: install.OLMManagedLabelValue,
	}
}

func (s *grpcCatalogSourceDecorator) Annotations() map[string]string {
	// TODO: Maybe something better than just a copy of all annotations would be to have a specific 'podMetadata' section in the CatalogSource?
	return s.GetAnnotations()
}

func (s *grpcCatalogSourceDecorator) Service() (*corev1.Service, error) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      strings.ReplaceAll(s.GetName(), ".", "-"),
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
	hash, err := hashutil.DeepHashObject(&svc.Spec)
	if err != nil {
		return nil, err
	}
	labels[ServiceHashLabelKey] = hash
	labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue
	svc.SetLabels(labels)
	ownerutil.AddOwner(svc, s.CatalogSource, false, false)
	return svc, nil
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
			Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue},
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

func (s *grpcCatalogSourceDecorator) Pod(serviceAccount *corev1.ServiceAccount) (*corev1.Pod, error) {
	pod, err := Pod(s.CatalogSource, "registry-server", s.opmImage, s.utilImage, s.Spec.Image, serviceAccount, s.Labels(), s.Annotations(), 5, 10, s.createPodAsUser)
	if err != nil {
		return nil, err
	}
	ownerutil.AddOwner(pod, s.CatalogSource, false, true)
	return pod, nil
}

type GrpcRegistryReconciler struct {
	now             nowFunc
	Lister          operatorlister.OperatorLister
	OpClient        operatorclient.ClientInterface
	SSAClient       *controllerclient.ServerSideApplier
	createPodAsUser int64
	opmImage        string
	utilImage       string
}

var _ RegistryReconciler = &GrpcRegistryReconciler{}

func (c *GrpcRegistryReconciler) currentService(source grpcCatalogSourceDecorator) (*corev1.Service, error) {
	protoService, err := source.Service()
	if err != nil {
		return nil, err
	}
	serviceName := protoService.GetName()
	service, err := c.Lister.CoreV1().ServiceLister().Services(source.GetNamespace()).Get(serviceName)
	if err != nil {
		logrus.WithField("service", serviceName).Debug("couldn't find service in cache")
		return nil, nil
	}
	return service, nil
}

func (c *GrpcRegistryReconciler) currentServiceAccount(source grpcCatalogSourceDecorator) *corev1.ServiceAccount {
	serviceAccountName := source.ServiceAccount().GetName()
	serviceAccount, err := c.Lister.CoreV1().ServiceAccountLister().ServiceAccounts(source.GetNamespace()).Get(serviceAccountName)
	if err != nil {
		logrus.WithField("serviceAccount", serviceAccount).Debug("couldn't find serviceAccount in cache")
		return nil
	}
	return serviceAccount
}

func (c *GrpcRegistryReconciler) currentPods(logger *logrus.Entry, source grpcCatalogSourceDecorator) []*corev1.Pod {
	pods, err := c.Lister.CoreV1().PodLister().Pods(source.GetNamespace()).List(source.Selector())
	if err != nil {
		logger.WithError(err).Warn("couldn't find pod in cache")
		return nil
	}
	if len(pods) > 1 {
		logger.WithField("selector", source.Selector()).Info("multiple pods found for selector")
	}
	return pods
}

func (c *GrpcRegistryReconciler) currentUpdatePods(logger *logrus.Entry, source grpcCatalogSourceDecorator) []*corev1.Pod {
	pods, err := c.Lister.CoreV1().PodLister().Pods(source.GetNamespace()).List(source.SelectorForUpdate())
	if err != nil {
		logger.WithError(err).Warn("couldn't find pod in cache")
		return nil
	}
	if len(pods) > 1 {
		logger.WithField("selector", source.Selector()).Info("multiple update pods found for selector")
	}
	return pods
}

func (c *GrpcRegistryReconciler) currentPodsWithCorrectImageAndSpec(logger *logrus.Entry, source grpcCatalogSourceDecorator, serviceAccount *corev1.ServiceAccount) ([]*corev1.Pod, error) {
	logger.Info("searching for current pods")
	pods, err := c.Lister.CoreV1().PodLister().Pods(source.GetNamespace()).List(labels.SelectorFromValidatedSet(source.Labels()))
	if err != nil {
		logger.WithError(err).Warn("couldn't find pod in cache")
		return nil, nil
	}
	found := []*corev1.Pod{}
	newPod, err := source.Pod(serviceAccount)
	if err != nil {
		return nil, err
	}
	for _, p := range pods {
		images, hash := correctImages(source, p), podHashMatch(p, newPod)
		logger = logger.WithFields(logrus.Fields{
			"current-pod.namespace": p.Namespace, "current-pod.name": p.Name,
			"correctImages": images, "correctHash": hash,
		})
		logger.Info("evaluating current pod")
		if !hash {
			logger.Infof("pod spec diff: %s", cmp.Diff(p.Spec, newPod.Spec))
		}
		if correctImages(source, p) && podHashMatch(p, newPod) {
			found = append(found, p)
		}
	}
	logger.Infof("of %d pods matching label selector, %d have the correct images and matching hash", len(pods), len(found))
	return found, nil
}

func correctImages(source grpcCatalogSourceDecorator, pod *corev1.Pod) bool {
	if source.CatalogSource.Spec.GrpcPodConfig != nil && source.CatalogSource.Spec.GrpcPodConfig.ExtractContent != nil {
		if len(pod.Spec.InitContainers) != 2 {
			return false
		}
		if len(pod.Spec.Containers) != 1 {
			return false
		}
		return pod.Spec.InitContainers[0].Image == source.utilImage &&
			pod.Spec.InitContainers[1].Image == source.CatalogSource.Spec.Image &&
			pod.Spec.Containers[0].Image == source.opmImage
	}
	return pod.Spec.Containers[0].Image == source.CatalogSource.Spec.Image
}

// EnsureRegistryServer ensures that all components of registry server are up to date.
func (c *GrpcRegistryReconciler) EnsureRegistryServer(logger *logrus.Entry, catalogSource *v1alpha1.CatalogSource) error {
	source := grpcCatalogSourceDecorator{CatalogSource: catalogSource, createPodAsUser: c.createPodAsUser, opmImage: c.opmImage, utilImage: c.utilImage}

	// if service status is nil, we force create every object to ensure they're created the first time
	valid, err := isRegistryServiceStatusValid(&source)
	if err != nil {
		return err
	}
	overwrite := !valid
	if overwrite {
		logger.Info("registry service status invalid, need to overwrite")
	}

	//TODO: if any of these error out, we should write a status back (possibly set RegistryServiceStatus to nil so they get recreated)
	sa, err := c.ensureSA(source)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return pkgerrors.Wrapf(err, "error ensuring service account: %s", source.GetName())
	}

	sa, err = c.OpClient.GetServiceAccount(sa.GetNamespace(), sa.GetName())
	if err != nil {
		return err
	}

	// recreate the pod if no existing pod is serving the latest image or correct spec
	current, err := c.currentPodsWithCorrectImageAndSpec(logger, source, sa)
	if err != nil {
		return err
	}
	overwritePod := overwrite || len(current) == 0
	if overwritePod {
		logger.Info("registry pods invalid, need to overwrite")
	}

	pod, err := source.Pod(sa)
	if err != nil {
		return err
	}
	if err := c.ensurePod(logger, source, sa, overwritePod); err != nil {
		return pkgerrors.Wrapf(err, "error ensuring pod: %s", pod.GetName())
	}
	if err := c.ensureUpdatePod(logger, sa, source); err != nil {
		if _, ok := err.(UpdateNotReadyErr); ok {
			return err
		}
		return pkgerrors.Wrapf(err, "error ensuring updated catalog source pod: %s", pod.GetName())
	}
	service, err := source.Service()
	if err != nil {
		return err
	}
	if err := c.ensureService(source, overwrite); err != nil {
		return pkgerrors.Wrapf(err, "error ensuring service: %s", service.GetName())
	}

	if overwritePod {
		now := c.now()
		service, err := source.Service()
		if err != nil {
			return err
		}
		catalogSource.Status.RegistryServiceStatus = &v1alpha1.RegistryServiceStatus{
			CreatedAt:        now,
			Protocol:         "grpc",
			ServiceName:      service.GetName(),
			ServiceNamespace: source.GetNamespace(),
			Port:             getPort(service),
		}
	}
	return nil
}

func getPort(service *corev1.Service) string {
	return fmt.Sprintf("%d", service.Spec.Ports[0].Port)
}

func isRegistryServiceStatusValid(source *grpcCatalogSourceDecorator) (bool, error) {
	service, err := source.Service()
	if err != nil {
		return false, err
	}
	if source.Status.RegistryServiceStatus == nil ||
		source.Status.RegistryServiceStatus.ServiceName != service.GetName() ||
		source.Status.RegistryServiceStatus.ServiceNamespace != service.GetNamespace() ||
		source.Status.RegistryServiceStatus.Port != getPort(service) ||
		source.Status.RegistryServiceStatus.Protocol != "grpc" {
		return false, nil
	}
	return true, nil
}

func (c *GrpcRegistryReconciler) ensurePod(logger *logrus.Entry, source grpcCatalogSourceDecorator, serviceAccount *corev1.ServiceAccount, overwrite bool) error {
	// currentPods refers to the current pod instances of the catalog source
	currentPods := c.currentPods(logger, source)

	var forceDeleteErrs []error
	currentPods = slices.DeleteFunc(currentPods, func(pod *corev1.Pod) bool {
		if !isPodDead(pod) {
			logger.WithFields(logrus.Fields{"pod.namespace": source.GetNamespace(), "pod.name": pod.GetName()}).Debug("pod is alive")
			return false
		}
		logger.WithFields(logrus.Fields{"pod.namespace": source.GetNamespace(), "pod.name": pod.GetName()}).Info("force deleting dead pod")
		if err := c.OpClient.KubernetesInterface().CoreV1().Pods(source.GetNamespace()).Delete(context.TODO(), pod.GetName(), metav1.DeleteOptions{
			GracePeriodSeconds: ptr.To[int64](0),
		}); err != nil && !apierrors.IsNotFound(err) {
			forceDeleteErrs = append(forceDeleteErrs, pkgerrors.Wrapf(err, "error deleting old pod: %s", pod.GetName()))
		}
		return true
	})
	if len(forceDeleteErrs) > 0 {
		return errors.Join(forceDeleteErrs...)
	}

	if len(currentPods) > 0 {
		if !overwrite {
			return nil
		}
		for _, p := range currentPods {
			logger.WithFields(logrus.Fields{"pod.namespace": source.GetNamespace(), "pod.name": p.GetName()}).Info("deleting current pod")
			if err := c.OpClient.KubernetesInterface().CoreV1().Pods(source.GetNamespace()).Delete(context.TODO(), p.GetName(), *metav1.NewDeleteOptions(1)); err != nil && !apierrors.IsNotFound(err) {
				return pkgerrors.Wrapf(err, "error deleting old pod: %s", p.GetName())
			}
		}
	}
	desiredPod, err := source.Pod(serviceAccount)
	if err != nil {
		return err
	}
	logger.WithFields(logrus.Fields{"pod.namespace": desiredPod.GetNamespace(), "pod.name": desiredPod.GetName()}).Info("creating desired pod")
	_, err = c.OpClient.KubernetesInterface().CoreV1().Pods(source.GetNamespace()).Create(context.TODO(), desiredPod, metav1.CreateOptions{})
	if err != nil {
		return pkgerrors.Wrapf(err, "error creating new pod: %s", desiredPod.GetGenerateName())
	}

	return nil
}

// ensureUpdatePod checks that for the same catalog source version the same container imageID is running
func (c *GrpcRegistryReconciler) ensureUpdatePod(logger *logrus.Entry, serviceAccount *corev1.ServiceAccount, source grpcCatalogSourceDecorator) error {
	if !source.Poll() {
		logger.Info("polling not enabled, no update pod will be created")
		return nil
	}

	currentLivePods := c.currentPods(logger, source)
	currentUpdatePods := c.currentUpdatePods(logger, source)

	if source.Update() && len(currentUpdatePods) == 0 {
		logger.Infof("catalog update required at %s", time.Now().String())
		pod, err := c.createUpdatePod(source, serviceAccount)
		if err != nil {
			return pkgerrors.Wrapf(err, "creating update catalog source pod")
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
		if imageChanged(logger, updatePod, currentLivePods) {
			err := c.promoteCatalog(updatePod, source.GetName())
			if err != nil {
				return fmt.Errorf("detected imageID change: error during update: %s", err)
			}
			// remove old catalog source pod
			for _, p := range currentLivePods {
				logger.WithFields(logrus.Fields{"live-pod.namespace": source.GetNamespace(), "live-pod.name": p.Name}).Info("deleting current live pods")
				if err := c.OpClient.KubernetesInterface().CoreV1().Pods(source.GetNamespace()).Delete(context.TODO(), p.GetName(), *metav1.NewDeleteOptions(1)); err != nil && !apierrors.IsNotFound(err) {
					return pkgerrors.Wrapf(pkgerrors.Wrapf(err, "error deleting pod: %s", p.GetName()), "detected imageID change: error deleting old catalog source pod")
				}
			}
			// done syncing
			logger.Infof("detected imageID change: catalogsource pod updated at %s", time.Now().String())
			return nil
		}
		// delete update pod right away, since the digest match, to prevent long-lived duplicate catalog pods
		logger.WithFields(logrus.Fields{"update-pod.namespace": updatePod.Namespace, "update-pod.name": updatePod.Name}).Debug("catalog polling result: no update; removing duplicate update pod")
		if err := c.OpClient.KubernetesInterface().CoreV1().Pods(source.GetNamespace()).Delete(context.TODO(), updatePod.GetName(), *metav1.NewDeleteOptions(1)); err != nil && !apierrors.IsNotFound(err) {
			return pkgerrors.Wrapf(pkgerrors.Wrapf(err, "error deleting pod: %s", updatePod.GetName()), "duplicate catalog polling pod")
		}
	}

	return nil
}

func (c *GrpcRegistryReconciler) ensureService(source grpcCatalogSourceDecorator, overwrite bool) error {
	service, err := source.Service()
	if err != nil {
		return err
	}
	svc, err := c.currentService(source)
	if err != nil {
		return err
	}
	if svc != nil {
		if !overwrite && ServiceHashMatch(svc, service) {
			return nil
		}
		// TODO(tflannag): Do we care about force deleting services?
		if err := c.OpClient.DeleteService(service.GetNamespace(), service.GetName(), metav1.NewDeleteOptions(0)); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	_, err = c.OpClient.CreateService(service)
	return err
}

func (c *GrpcRegistryReconciler) ensureSA(source grpcCatalogSourceDecorator) (*corev1.ServiceAccount, error) {
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

// createUpdatePod is an internal method that creates a pod using the latest catalog source.
func (c *GrpcRegistryReconciler) createUpdatePod(source grpcCatalogSourceDecorator, serviceAccount *corev1.ServiceAccount) (*corev1.Pod, error) {
	// remove label from pod to ensure service does not accidentally route traffic to the pod
	p, err := source.Pod(serviceAccount)
	if err != nil {
		return nil, err
	}
	p = swapLabels(p, "", source.Name)

	pod, err := c.OpClient.KubernetesInterface().CoreV1().Pods(source.GetNamespace()).Create(context.TODO(), p, metav1.CreateOptions{})
	if err != nil {
		logrus.WithField("pod", p.GetName()).Warn("couldn't create new catalogsource pod")
		return nil, err
	}

	return pod, nil
}

// checkUpdatePodDigest checks update pod to get Image ID and see if it matches the serving (live) pod ImageID
func imageChanged(logger *logrus.Entry, updatePod *corev1.Pod, servingPods []*corev1.Pod) bool {
	updatedCatalogSourcePodImageID := imageID(updatePod)
	if updatedCatalogSourcePodImageID == "" {
		logger.WithField("update-pod.name", updatePod.GetName()).Warn("pod status unknown, cannot get the updated pod's imageID")
		return false
	}
	for _, servingPod := range servingPods {
		servingCatalogSourcePodImageID := imageID(servingPod)
		if servingCatalogSourcePodImageID == "" {
			logger.WithField("serving-pod.name", servingPod.GetName()).Warn("pod status unknown, cannot get the current pod's imageID")
			return false
		}
		if updatedCatalogSourcePodImageID != servingCatalogSourcePodImageID {
			logger.WithField("serving-pod.name", servingPod.GetName()).Infof("catalog image changed: serving pod %s update pod %s", servingCatalogSourcePodImageID, updatedCatalogSourcePodImageID)
			return true
		}
	}

	return false
}

func isPodDead(pod *corev1.Pod) bool {
	for _, check := range []func(*corev1.Pod) bool{
		isPodDeletedByTaintManager,
	} {
		if check(pod) {
			return true
		}
	}
	return false
}

func isPodDeletedByTaintManager(pod *corev1.Pod) bool {
	if pod.DeletionTimestamp == nil {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.DisruptionTarget && condition.Reason == "DeletionByTaintManager" && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// imageID returns the ImageID of the primary catalog source container or an empty string if the image ID isn't available yet.
// Note: the pod must be running and the container in a ready status to return a valid ImageID.
func imageID(pod *corev1.Pod) string {
	if len(pod.Status.InitContainerStatuses) == 2 && len(pod.Status.ContainerStatuses) == 1 {
		// spec.grpcPodConfig.extractContent mode was used for this pod
		return pod.Status.InitContainerStatuses[1].ImageID
	}
	if len(pod.Status.InitContainerStatuses) == 0 && len(pod.Status.ContainerStatuses) == 1 {
		// spec.grpcPodConfig.extractContent mode was NOT used for this pod (i.e. we're just running the catalog image directly)
		return pod.Status.ContainerStatuses[0].ImageID
	}
	if len(pod.Status.InitContainerStatuses) == 0 && len(pod.Status.ContainerStatuses) == 0 {
		logrus.WithField("CatalogSource", pod.GetName()).Warn("pod status unknown; pod has not yet populated initContainer and container status")
	} else {
		logrus.WithField("CatalogSource", pod.GetName()).Warn("pod status unknown; pod contains unexpected initContainer and container configuration")
	}
	return ""
}

func (c *GrpcRegistryReconciler) removePods(pods []*corev1.Pod, namespace string) error {
	for _, p := range pods {
		if err := c.OpClient.KubernetesInterface().CoreV1().Pods(namespace).Delete(context.TODO(), p.GetName(), *metav1.NewDeleteOptions(1)); err != nil && !apierrors.IsNotFound(err) {
			return pkgerrors.Wrapf(err, "error deleting pod: %s", p.GetName())
		}
	}
	return nil
}

// CheckRegistryServer returns true if the given CatalogSource is considered healthy; false otherwise.
func (c *GrpcRegistryReconciler) CheckRegistryServer(logger *logrus.Entry, catalogSource *v1alpha1.CatalogSource) (bool, error) {
	source := grpcCatalogSourceDecorator{CatalogSource: catalogSource, createPodAsUser: c.createPodAsUser, opmImage: c.opmImage, utilImage: c.utilImage}

	// The CheckRegistryServer function is called by the CatalogSoruce controller before the registry resources are created,
	// returning a IsNotFound error will cause the controller to exit and never create the resources, so we should
	// only return an error if it is something other than a NotFound error.
	serviceAccount := source.ServiceAccount()
	serviceAccount, err := c.OpClient.GetServiceAccount(serviceAccount.GetNamespace(), serviceAccount.GetName())
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return false, err
		}
		return false, nil
	}

	// Check on registry resources
	// TODO: add gRPC health check
	service, err := c.currentService(source)
	if err != nil {
		return false, err
	}
	current, err := c.currentPodsWithCorrectImageAndSpec(logger, source, serviceAccount)
	if err != nil {
		return false, err
	}
	if len(current) < 1 ||
		service == nil || c.currentServiceAccount(source) == nil {
		return false, nil
	}

	return true, nil
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
			return true, pkgerrors.Wrapf(err, "error deleting failed catalog polling pod: %s", pod.GetName())
		}
		return true, nil
	}
	return false, nil
}

// podHashMatch will check the hash info in existing pod to ensure its
// hash info matches the desired Service's hash.
func podHashMatch(existing, new *corev1.Pod) bool {
	labels := existing.GetLabels()
	newLabels := new.GetLabels()
	// If both new & existing pods don't have labels, consider it not matched
	if len(labels) == 0 || len(newLabels) == 0 {
		return false
	}

	existingPodSpecHash, ok := labels[PodHashLabelKey]
	if !ok {
		return false
	}

	newPodSpecHash, ok := newLabels[PodHashLabelKey]
	if !ok {
		return false
	}

	if existingPodSpecHash != newPodSpecHash {
		return false
	}

	return true
}

package bundle

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/configmap"
	"github.com/sirupsen/logrus"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/client-go/kubernetes"
	listersbatchv1 "k8s.io/client-go/listers/batch/v1"
	listerscorev1 "k8s.io/client-go/listers/core/v1"
	listersrbacv1 "k8s.io/client-go/listers/rbac/v1"
	"k8s.io/utils/ptr"

	"github.com/operator-framework/api/pkg/operators/reference"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	v1listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1"
	listersoperatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/projection"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/image"
)

const (
	// TODO: This can be a spec field
	// BundleUnpackTimeoutAnnotationKey allows setting a bundle unpack timeout per OperatorGroup
	// and overrides the default specified by the --bundle-unpack-timeout flag
	// The time duration should be in the same format as accepted by time.ParseDuration()
	// e.g 1m30s
	BundleUnpackTimeoutAnnotationKey = "operatorframework.io/bundle-unpack-timeout"
	BundleUnpackPodLabel             = "job-name"

	// BundleUnpackRetryMinimumIntervalAnnotationKey sets a minimum interval to wait before
	// attempting to recreate a failed unpack job for a bundle.
	BundleUnpackRetryMinimumIntervalAnnotationKey = "operatorframework.io/bundle-unpack-min-retry-interval"

	// bundleUnpackRefLabel is used to filter for all unpack jobs for a specific bundle.
	bundleUnpackRefLabel = "operatorframework.io/bundle-unpack-ref"
)

type BundleUnpackResult struct {
	*operatorsv1alpha1.BundleLookup

	bundle *api.Bundle
	name   string
}

func (b *BundleUnpackResult) Bundle() *api.Bundle {
	return b.bundle
}

func (b *BundleUnpackResult) Name() string {
	return b.name
}

// SetCondition replaces the existing BundleLookupCondition of the same type, or adds it if it was not found.
func (b *BundleUnpackResult) SetCondition(cond operatorsv1alpha1.BundleLookupCondition) operatorsv1alpha1.BundleLookupCondition {
	for i, existing := range b.Conditions {
		if existing.Type != cond.Type {
			continue
		}
		if existing.Status == cond.Status && existing.Reason == cond.Reason {
			cond.LastTransitionTime = existing.LastTransitionTime
		}
		b.Conditions[i] = cond
		return cond
	}
	b.Conditions = append(b.Conditions, cond)

	return cond
}

var catalogSourceGVK = operatorsv1alpha1.SchemeGroupVersion.WithKind(operatorsv1alpha1.CatalogSourceKind)

func newBundleUnpackResult(lookup *operatorsv1alpha1.BundleLookup) *BundleUnpackResult {
	return &BundleUnpackResult{
		BundleLookup: lookup.DeepCopy(),
		name:         hash(lookup.Path),
	}
}

func (c *ConfigMapUnpacker) job(cmRef *corev1.ObjectReference, bundlePath string, secrets []corev1.LocalObjectReference, annotationUnpackTimeout time.Duration) *batchv1.Job {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				install.OLMManagedLabelKey: install.OLMManagedLabelValue,
				bundleUnpackRefLabel:       cmRef.Name,
			},
		},
		Spec: batchv1.JobSpec{
			//ttlSecondsAfterFinished: 0 // can use in the future to not have to clean up job
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: cmRef.Name,
					Labels: map[string]string{
						install.OLMManagedLabelKey: install.OLMManagedLabelValue,
					},
				},
				Spec: corev1.PodSpec{
					// With restartPolicy = "OnFailure" when the spec.backoffLimit is reached, the job controller will delete all
					// the job's pods to stop them from crashlooping forever.
					// By setting restartPolicy = "Never" the pods don't get cleaned up since they're not running after a failure.
					// Keeping the pods around after failures helps in inspecting the logs of a failed bundle unpack job.
					// See: https://kubernetes.io/docs/concepts/workloads/controllers/job/#pod-backoff-failure-policy
					RestartPolicy:    corev1.RestartPolicyNever,
					ImagePullSecrets: secrets,
					SecurityContext: &corev1.PodSecurityContext{
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "extract",
							Image: c.opmImage,
							Command: []string{"opm", "alpha", "bundle", "extract",
								"-m", "/bundle/",
								"-n", cmRef.Namespace,
								"-c", cmRef.Name,
								"-z",
							},
							Env: []corev1.EnvVar{
								{
									Name:  configmap.EnvContainerImage,
									Value: bundlePath,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "bundle", // Expected bundle content mount
									MountPath: "/bundle",
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("50Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: ptr.To(bool(false)),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
							TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
						},
					},
					InitContainers: []corev1.Container{
						{
							Name:    "util",
							Image:   c.utilImage,
							Command: []string{"/bin/cp", "-Rv", "/bin/cpb", "/util/cpb"}, // Copy tooling for the bundle container to use
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "util",
									MountPath: "/util",
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("50Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: ptr.To(bool(false)),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
							TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
						},
						{
							Name:            "pull",
							Image:           bundlePath,
							ImagePullPolicy: image.InferImagePullPolicy(bundlePath),
							Command:         []string{"/util/cpb", "/bundle"}, // Copy bundle content to its mount
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "bundle",
									MountPath: "/bundle",
								},
								{
									Name:      "util",
									MountPath: "/util",
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("50Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: ptr.To(bool(false)),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
							TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "bundle", // Used to share bundle content
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "util", // Used to share utils
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
					NodeSelector: map[string]string{
						"kubernetes.io/os": "linux",
					},
					Tolerations: []corev1.Toleration{
						{
							Key:      "kubernetes.io/arch",
							Value:    "amd64",
							Operator: "Equal",
						},
						{
							Key:      "kubernetes.io/arch",
							Value:    "arm64",
							Operator: "Equal",
						},
						{
							Key:      "kubernetes.io/arch",
							Value:    "ppc64le",
							Operator: "Equal",
						},
						{
							Key:      "kubernetes.io/arch",
							Value:    "s390x",
							Operator: "Equal",
						},
					},
				},
			},
		},
	}
	job.SetNamespace(cmRef.Namespace)
	job.SetName(cmRef.Name)
	job.SetOwnerReferences([]metav1.OwnerReference{ownerRef(cmRef)})
	if c.runAsUser > 0 {
		job.Spec.Template.Spec.SecurityContext.RunAsUser = &c.runAsUser
		job.Spec.Template.Spec.SecurityContext.RunAsNonRoot = ptr.To(bool(true))
	}
	// By default the BackoffLimit is set to 6 which with exponential backoff 10s + 20s + 40s ...
	// translates to ~10m of waiting time.
	// We want to fail faster than that when we have repeated failures from the bundle unpack pod
	// so we set it to 3 which is ~1m of waiting time
	// See: https://kubernetes.io/docs/concepts/workloads/controllers/job/#pod-backoff-failure-policy
	backOffLimit := int32(3)
	job.Spec.BackoffLimit = &backOffLimit

	// Set ActiveDeadlineSeconds as the unpack timeout
	// Don't set a timeout if it is 0
	if c.unpackTimeout != time.Duration(0) {
		t := int64(c.unpackTimeout.Seconds())
		job.Spec.ActiveDeadlineSeconds = &t
	}

	// Check annotationUnpackTimeout which is the annotation override for the default unpack timeout
	// A negative timeout means the annotation was unset or malformed so we ignore it
	if annotationUnpackTimeout < time.Duration(0) {
		return job
	}
	// // 0 means no timeout so we unset ActiveDeadlineSeconds
	if annotationUnpackTimeout == time.Duration(0) {
		job.Spec.ActiveDeadlineSeconds = nil
		return job
	}

	timeoutSeconds := int64(annotationUnpackTimeout.Seconds())
	job.Spec.ActiveDeadlineSeconds = &timeoutSeconds

	return job
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . Unpacker

type Unpacker interface {
	UnpackBundle(lookup *operatorsv1alpha1.BundleLookup, timeout, retryInterval time.Duration) (result *BundleUnpackResult, err error)
}

type ConfigMapUnpacker struct {
	logger        *logrus.Logger
	opmImage      string
	utilImage     string
	client        kubernetes.Interface
	csLister      listersoperatorsv1alpha1.CatalogSourceLister
	cmLister      listerscorev1.ConfigMapLister
	jobLister     listersbatchv1.JobLister
	podLister     listerscorev1.PodLister
	roleLister    listersrbacv1.RoleLister
	rbLister      listersrbacv1.RoleBindingLister
	loader        *configmap.BundleLoader
	now           func() metav1.Time
	unpackTimeout time.Duration
	runAsUser     int64
}

type ConfigMapUnpackerOption func(*ConfigMapUnpacker)

func NewConfigmapUnpacker(options ...ConfigMapUnpackerOption) (*ConfigMapUnpacker, error) {
	unpacker := &ConfigMapUnpacker{
		loader: configmap.NewBundleLoader(),
	}

	unpacker.apply(options...)
	if err := unpacker.validate(); err != nil {
		return nil, err
	}

	return unpacker, nil
}

func WithUnpackTimeout(timeout time.Duration) ConfigMapUnpackerOption {
	return func(unpacker *ConfigMapUnpacker) {
		unpacker.unpackTimeout = timeout
	}
}

func WithOPMImage(opmImage string) ConfigMapUnpackerOption {
	return func(unpacker *ConfigMapUnpacker) {
		unpacker.opmImage = opmImage
	}
}

func WithUtilImage(utilImage string) ConfigMapUnpackerOption {
	return func(unpacker *ConfigMapUnpacker) {
		unpacker.utilImage = utilImage
	}
}

func WithLogger(logger *logrus.Logger) ConfigMapUnpackerOption {
	return func(unpacker *ConfigMapUnpacker) {
		unpacker.logger = logger
	}
}

func WithClient(client kubernetes.Interface) ConfigMapUnpackerOption {
	return func(unpacker *ConfigMapUnpacker) {
		unpacker.client = client
	}
}

func WithCatalogSourceLister(csLister listersoperatorsv1alpha1.CatalogSourceLister) ConfigMapUnpackerOption {
	return func(unpacker *ConfigMapUnpacker) {
		unpacker.csLister = csLister
	}
}

func WithConfigMapLister(cmLister listerscorev1.ConfigMapLister) ConfigMapUnpackerOption {
	return func(unpacker *ConfigMapUnpacker) {
		unpacker.cmLister = cmLister
	}
}

func WithJobLister(jobLister listersbatchv1.JobLister) ConfigMapUnpackerOption {
	return func(unpacker *ConfigMapUnpacker) {
		unpacker.jobLister = jobLister
	}
}

func WithPodLister(podLister listerscorev1.PodLister) ConfigMapUnpackerOption {
	return func(unpacker *ConfigMapUnpacker) {
		unpacker.podLister = podLister
	}
}

func WithRoleLister(roleLister listersrbacv1.RoleLister) ConfigMapUnpackerOption {
	return func(unpacker *ConfigMapUnpacker) {
		unpacker.roleLister = roleLister
	}
}

func WithRoleBindingLister(rbLister listersrbacv1.RoleBindingLister) ConfigMapUnpackerOption {
	return func(unpacker *ConfigMapUnpacker) {
		unpacker.rbLister = rbLister
	}
}

func WithNow(now func() metav1.Time) ConfigMapUnpackerOption {
	return func(unpacker *ConfigMapUnpacker) {
		unpacker.now = now
	}
}

func WithUserID(id int64) ConfigMapUnpackerOption {
	return func(unpacker *ConfigMapUnpacker) {
		unpacker.runAsUser = id
	}
}

func (c *ConfigMapUnpacker) apply(options ...ConfigMapUnpackerOption) {
	for _, option := range options {
		option(c)
	}
}

func (c *ConfigMapUnpacker) validate() (err error) {
	switch {
	case c.opmImage == "":
		err = fmt.Errorf("no opm image given")
	case c.utilImage == "":
		err = fmt.Errorf("no util image given")
	case c.client == nil:
		err = fmt.Errorf("client is nil")
	case c.csLister == nil:
		err = fmt.Errorf("catalogsource lister is nil")
	case c.cmLister == nil:
		err = fmt.Errorf("configmap lister is nil")
	case c.jobLister == nil:
		err = fmt.Errorf("job lister is nil")
	case c.podLister == nil:
		err = fmt.Errorf("pod lister is nil")
	case c.roleLister == nil:
		err = fmt.Errorf("role lister is nil")
	case c.rbLister == nil:
		err = fmt.Errorf("rolebinding lister is nil")
	case c.loader == nil:
		err = fmt.Errorf("bundle loader is nil")
	case c.now == nil:
		err = fmt.Errorf("now func is nil")
	}

	return
}

const (
	CatalogSourceMissingReason  = "CatalogSourceMissing"
	CatalogSourceMissingMessage = "referenced catalogsource not found"
	JobFailedReason             = "JobFailed"
	JobFailedMessage            = "unpack job has failed"
	JobIncompleteReason         = "JobIncomplete"
	JobIncompleteMessage        = "unpack job not completed"
	JobNotStartedReason         = "JobNotStarted"
	JobNotStartedMessage        = "unpack job not yet started"
	NotUnpackedReason           = "BundleNotUnpacked"
	NotUnpackedMessage          = "bundle contents have not yet been persisted to installplan status"
)

func (c *ConfigMapUnpacker) UnpackBundle(lookup *operatorsv1alpha1.BundleLookup, timeout, retryInterval time.Duration) (result *BundleUnpackResult, err error) {
	result = newBundleUnpackResult(lookup)

	// if bundle lookup failed condition already present, then there is nothing more to do
	failedCond := result.GetCondition(operatorsv1alpha1.BundleLookupFailed)
	if failedCond.Status == corev1.ConditionTrue {
		return result, nil
	}

	// if pending condition is not true then bundle has already been unpacked(unknown)
	pendingCond := result.GetCondition(operatorsv1alpha1.BundleLookupPending)
	if pendingCond.Status != corev1.ConditionTrue {
		return result, nil
	}

	now := c.now()

	var cs *operatorsv1alpha1.CatalogSource
	if cs, err = c.csLister.CatalogSources(result.CatalogSourceRef.Namespace).Get(result.CatalogSourceRef.Name); err != nil {
		if apierrors.IsNotFound(err) && pendingCond.Reason != CatalogSourceMissingReason {
			pendingCond.Status = corev1.ConditionTrue
			pendingCond.Reason = CatalogSourceMissingReason
			pendingCond.Message = CatalogSourceMissingMessage
			pendingCond.LastTransitionTime = &now
			result.SetCondition(pendingCond)
			err = nil
		}

		return
	}

	// Add missing info to the object reference
	csRef := result.CatalogSourceRef.DeepCopy()
	csRef.SetGroupVersionKind(catalogSourceGVK)
	csRef.UID = cs.GetUID()

	cm, err := c.ensureConfigmap(csRef, result.name)
	if err != nil {
		return
	}

	var cmRef *corev1.ObjectReference
	cmRef, err = reference.GetReference(cm)
	if err != nil {
		return
	}

	_, err = c.ensureRole(cmRef)
	if err != nil {
		return
	}

	_, err = c.ensureRoleBinding(cmRef)
	if err != nil {
		return
	}

	secrets := make([]corev1.LocalObjectReference, 0)
	for _, secretName := range cs.Spec.Secrets {
		secrets = append(secrets, corev1.LocalObjectReference{Name: secretName})
	}
	var job *batchv1.Job
	job, err = c.ensureJob(cmRef, result.Path, secrets, timeout, retryInterval)
	if err != nil || job == nil {
		// ensureJob can return nil if the job present does not match the expected job (spec and ownerefs)
		// The current job is deleted in that case so UnpackBundle needs to be retried
		return
	}

	// Check if bundle unpack job has failed due a timeout
	// Return a BundleJobError so we can mark the InstallPlan as Failed
	if jobCond, isFailed := getCondition(job, batchv1.JobFailed); isFailed {
		// Add the BundleLookupFailed condition with the message and reason from the job failure
		failedCond.Status = corev1.ConditionTrue
		failedCond.Reason = jobCond.Reason
		failedCond.Message = jobCond.Message
		failedCond.LastTransitionTime = &now
		result.SetCondition(failedCond)

		return
	}

	if _, isComplete := getCondition(job, batchv1.JobComplete); !isComplete {
		// In the case of an image pull failure for a non-existent image the bundle unpack job
		// can stay pending until the ActiveDeadlineSeconds timeout ~10m
		// To indicate why it's pending we inspect the container statuses of the
		// unpack Job pods to surface that information on the bundle lookup conditions
		pendingMessage := JobIncompleteMessage
		var pendingContainerStatusMsgs string
		pendingContainerStatusMsgs, err = c.pendingContainerStatusMessages(job)
		if err != nil {
			return
		}

		if pendingContainerStatusMsgs != "" {
			pendingMessage = pendingMessage + ": " + pendingContainerStatusMsgs
		}

		// Update BundleLookupPending condition if there are any changes
		if pendingCond.Status != corev1.ConditionTrue || pendingCond.Reason != JobIncompleteReason || pendingCond.Message != pendingMessage {
			pendingCond.Status = corev1.ConditionTrue
			pendingCond.Reason = JobIncompleteReason
			pendingCond.Message = pendingMessage
			pendingCond.LastTransitionTime = &now
			result.SetCondition(pendingCond)
		}

		return
	}

	result.bundle, err = c.loader.Load(cm)
	if err != nil {
		return
	}

	if result.Bundle() == nil || len(result.Bundle().GetObject()) == 0 {
		return
	}

	if result.BundleLookup.Properties != "" {
		props, err := projection.PropertyListFromPropertiesAnnotation(lookup.Properties)
		if err != nil {
			return nil, fmt.Errorf("failed to load bundle properties for %q: %w", lookup.Identifier, err)
		}
		result.bundle.Properties = props
	}

	// A successful load should remove the pending condition
	result.RemoveCondition(operatorsv1alpha1.BundleLookupPending)

	return
}

func (c *ConfigMapUnpacker) pendingContainerStatusMessages(job *batchv1.Job) (string, error) {
	containerStatusMessages := []string{}
	// List pods for unpack job
	podLabel := map[string]string{BundleUnpackPodLabel: job.GetName()}
	pods, listErr := c.podLister.Pods(job.GetNamespace()).List(k8slabels.SelectorFromValidatedSet(podLabel))
	if listErr != nil {
		c.logger.Errorf("failed to list pods for job(%s): %v", job.GetName(), listErr)
		return "", fmt.Errorf("failed to list pods for job(%s): %v", job.GetName(), listErr)
	}

	// Ideally there should be just 1 pod running but inspect all pods in the pending phase
	// to see if any are stuck on an ImagePullBackOff or ErrImagePull error
	for _, pod := range pods {
		if pod.Status.Phase != corev1.PodPending {
			// skip status check for non-pending pods
			continue
		}

		for _, ic := range pod.Status.InitContainerStatuses {
			if ic.Ready {
				// only check non-ready containers for their waiting reasons
				continue
			}

			msg := fmt.Sprintf("Unpack pod(%s/%s) container(%s) is pending", pod.Namespace, pod.Name, ic.Name)
			waiting := ic.State.Waiting
			if waiting != nil {
				msg = fmt.Sprintf("Unpack pod(%s/%s) container(%s) is pending. Reason: %s, Message: %s",
					pod.Namespace, pod.Name, ic.Name, waiting.Reason, waiting.Message)
			}

			// Aggregate the wait reasons for all pending containers
			containerStatusMessages = append(containerStatusMessages, msg)
		}
	}

	return strings.Join(containerStatusMessages, " | "), nil
}

func (c *ConfigMapUnpacker) ensureConfigmap(csRef *corev1.ObjectReference, name string) (cm *corev1.ConfigMap, err error) {
	fresh := &corev1.ConfigMap{}
	fresh.SetNamespace(csRef.Namespace)
	fresh.SetName(name)
	fresh.SetOwnerReferences([]metav1.OwnerReference{ownerRef(csRef)})
	fresh.SetLabels(map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue})

	cm, err = c.cmLister.ConfigMaps(fresh.GetNamespace()).Get(fresh.GetName())
	if apierrors.IsNotFound(err) {
		cm, err = c.client.CoreV1().ConfigMaps(fresh.GetNamespace()).Create(context.TODO(), fresh, metav1.CreateOptions{})
		// CM already exists in cluster but not in cache, then add the label
		if err != nil && apierrors.IsAlreadyExists(err) {
			cm, err = c.client.CoreV1().ConfigMaps(fresh.GetNamespace()).Get(context.TODO(), fresh.GetName(), metav1.GetOptions{})
			if err != nil {
				return nil, fmt.Errorf("failed to retrieve configmap %s: %v", fresh.GetName(), err)
			}
			cm.SetLabels(map[string]string{
				install.OLMManagedLabelKey: install.OLMManagedLabelValue,
			})
			cm, err = c.client.CoreV1().ConfigMaps(cm.GetNamespace()).Update(context.TODO(), cm, metav1.UpdateOptions{})
			if err != nil {
				return nil, fmt.Errorf("failed to update configmap %s: %v", cm.GetName(), err)
			}
		}
	}

	return
}

func (c *ConfigMapUnpacker) ensureJob(cmRef *corev1.ObjectReference, bundlePath string, secrets []corev1.LocalObjectReference, timeout time.Duration, unpackRetryInterval time.Duration) (job *batchv1.Job, err error) {
	fresh := c.job(cmRef, bundlePath, secrets, timeout)
	var jobs, toDelete []*batchv1.Job
	jobs, err = c.jobLister.Jobs(fresh.GetNamespace()).List(k8slabels.ValidatedSetSelector{bundleUnpackRefLabel: cmRef.Name})
	if err != nil {
		return
	}

	// This is to ensure that we account for any existing unpack jobs that may be missing the label
	jobWithoutLabel, err := c.jobLister.Jobs(fresh.GetNamespace()).Get(cmRef.Name)
	if err != nil && !apierrors.IsNotFound(err) {
		return
	}
	if jobWithoutLabel != nil {
		_, labelExists := jobWithoutLabel.Labels[bundleUnpackRefLabel]
		if !labelExists {
			jobs = append(jobs, jobWithoutLabel)
		}
	}

	if len(jobs) == 0 {
		job, err = c.client.BatchV1().Jobs(fresh.GetNamespace()).Create(context.TODO(), fresh, metav1.CreateOptions{})
		return
	}

	maxRetainedJobs := 5                                  // TODO: make this configurable
	job, toDelete = sortUnpackJobs(jobs, maxRetainedJobs) // choose latest or on-failed job attempt

	// only check for retries if an unpackRetryInterval is specified
	if unpackRetryInterval > 0 {
		if _, isFailed := getCondition(job, batchv1.JobFailed); isFailed {
			// Look for other unpack jobs for the same bundle
			if cond, failed := getCondition(job, batchv1.JobFailed); failed {
				if time.Now().After(cond.LastTransitionTime.Time.Add(unpackRetryInterval)) {
					fresh.SetName(names.SimpleNameGenerator.GenerateName(fresh.GetName()))
					job, err = c.client.BatchV1().Jobs(fresh.GetNamespace()).Create(context.TODO(), fresh, metav1.CreateOptions{})
				}
			}

			// cleanup old failed jobs, but don't clean up successful jobs to avoid repeat unpacking
			for _, j := range toDelete {
				_ = c.client.BatchV1().Jobs(j.GetNamespace()).Delete(context.TODO(), j.GetName(), metav1.DeleteOptions{})
			}
			return
		}
	}

	if equality.Semantic.DeepDerivative(fresh.GetOwnerReferences(), job.GetOwnerReferences()) && equality.Semantic.DeepDerivative(fresh.Spec, job.Spec) {
		return
	}

	// TODO: Decide when to fail-out instead of deleting the job
	err = c.client.BatchV1().Jobs(job.GetNamespace()).Delete(context.TODO(), job.GetName(), metav1.DeleteOptions{})
	job = nil
	return
}

func (c *ConfigMapUnpacker) ensureRole(cmRef *corev1.ObjectReference) (role *rbacv1.Role, err error) {
	if cmRef == nil {
		return nil, fmt.Errorf("configmap reference is nil")
	}

	rule := rbacv1.PolicyRule{
		APIGroups: []string{
			"",
		},
		Verbs: []string{
			"create", "get", "update",
		},
		Resources: []string{
			"configmaps",
		},
		ResourceNames: []string{
			cmRef.Name,
		},
	}
	fresh := &rbacv1.Role{
		Rules: []rbacv1.PolicyRule{rule},
	}
	fresh.SetNamespace(cmRef.Namespace)
	fresh.SetName(cmRef.Name)
	fresh.SetOwnerReferences([]metav1.OwnerReference{ownerRef(cmRef)})
	fresh.SetLabels(map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue})

	role, err = c.roleLister.Roles(fresh.GetNamespace()).Get(fresh.GetName())
	if err != nil {
		if apierrors.IsNotFound(err) {
			role, err = c.client.RbacV1().Roles(fresh.GetNamespace()).Create(context.TODO(), fresh, metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(err) {
				role, err = c.client.RbacV1().Roles(fresh.GetNamespace()).Update(context.TODO(), fresh, metav1.UpdateOptions{})
			}
		}

		return
	}

	// Add the policy rule if necessary
	for _, existing := range role.Rules {
		if equality.Semantic.DeepDerivative(rule, existing) {
			return
		}
	}
	role = role.DeepCopy()
	role.Rules = append(role.Rules, rule)

	role, err = c.client.RbacV1().Roles(role.GetNamespace()).Update(context.TODO(), role, metav1.UpdateOptions{})

	return
}

func (c *ConfigMapUnpacker) ensureRoleBinding(cmRef *corev1.ObjectReference) (roleBinding *rbacv1.RoleBinding, err error) {
	fresh := &rbacv1.RoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      "default",
				Namespace: cmRef.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     cmRef.Name,
		},
	}
	fresh.SetNamespace(cmRef.Namespace)
	fresh.SetName(cmRef.Name)
	fresh.SetOwnerReferences([]metav1.OwnerReference{ownerRef(cmRef)})
	fresh.SetLabels(map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue})

	roleBinding, err = c.rbLister.RoleBindings(fresh.GetNamespace()).Get(fresh.GetName())
	if err != nil {
		if apierrors.IsNotFound(err) {
			roleBinding, err = c.client.RbacV1().RoleBindings(fresh.GetNamespace()).Create(context.TODO(), fresh, metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(err) {
				roleBinding, err = c.client.RbacV1().RoleBindings(fresh.GetNamespace()).Update(context.TODO(), fresh, metav1.UpdateOptions{})
			}
		}

		return
	}

	if equality.Semantic.DeepDerivative(fresh.Subjects, roleBinding.Subjects) && equality.Semantic.DeepDerivative(fresh.RoleRef, roleBinding.RoleRef) {
		return
	}

	// TODO: Decide when to fail-out instead of deleting the rbac
	err = c.client.RbacV1().RoleBindings(roleBinding.GetNamespace()).Delete(context.TODO(), roleBinding.GetName(), metav1.DeleteOptions{})
	roleBinding = nil

	return
}

// hash hashes data with sha256 and returns the hex string.
func hash(data string) string {
	// A SHA256 hash is 64 characters, which is within the 253 character limit for kube resource names
	h := fmt.Sprintf("%x", sha256.Sum256([]byte(data)))

	// Make the hash 63 characters instead to comply with the 63 character limit for labels
	return fmt.Sprintf(h[:len(h)-1])
}

var blockOwnerDeletion = false

// ownerRef converts an ObjectReference to an OwnerReference.
func ownerRef(ref *corev1.ObjectReference) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion:         ref.APIVersion,
		Kind:               ref.Kind,
		Name:               ref.Name,
		UID:                ref.UID,
		Controller:         &blockOwnerDeletion,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}
}

// getCondition returns true if the given job has the given condition with the given condition type true, and returns false otherwise.
// Also returns the condition if true
func getCondition(job *batchv1.Job, conditionType batchv1.JobConditionType) (condition *batchv1.JobCondition, isTrue bool) {
	if job == nil {
		return
	}

	for _, cond := range job.Status.Conditions {
		if cond.Type == conditionType && cond.Status == corev1.ConditionTrue {
			condition = &cond
			isTrue = true
			return
		}
	}
	return
}

func sortUnpackJobs(jobs []*batchv1.Job, maxRetainedJobs int) (latest *batchv1.Job, toDelete []*batchv1.Job) {
	if len(jobs) == 0 {
		return
	}
	// sort jobs so that latest job is first
	// with preference for non-failed jobs
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i] == nil || jobs[j] == nil {
			return jobs[i] != nil
		}
		condI, failedI := getCondition(jobs[i], batchv1.JobFailed)
		condJ, failedJ := getCondition(jobs[j], batchv1.JobFailed)
		if failedI != failedJ {
			return !failedI // non-failed job goes first
		}
		return condI.LastTransitionTime.After(condJ.LastTransitionTime.Time)
	})
	if jobs[0] == nil {
		// all nil jobs
		return
	}
	latest = jobs[0]
	nilJobsIndex := len(jobs) - 1
	for ; nilJobsIndex >= 0 && jobs[nilJobsIndex] == nil; nilJobsIndex-- {
	}

	jobs = jobs[:nilJobsIndex+1] // exclude nil jobs from list of jobs to delete
	if len(jobs) <= maxRetainedJobs {
		return
	}
	if maxRetainedJobs == 0 {
		toDelete = jobs[1:]
		return
	}

	// cleanup old failed jobs, n-1 recent jobs and the oldest job
	for i := 0; i < maxRetainedJobs && i+maxRetainedJobs < len(jobs)-1; i++ {
		toDelete = append(toDelete, jobs[maxRetainedJobs+i])
	}

	return
}

// OperatorGroupBundleUnpackTimeout returns bundle timeout from annotation if specified.
// If the timeout annotation is not set, return timeout < 0 which is subsequently ignored.
// This is to overrides the --bundle-unpack-timeout flag value on per-OperatorGroup basis.
func OperatorGroupBundleUnpackTimeout(ogLister v1listers.OperatorGroupNamespaceLister) (time.Duration, error) {
	ignoreTimeout := -1 * time.Minute

	ogs, err := ogLister.List(k8slabels.Everything())
	if err != nil {
		return ignoreTimeout, err
	}
	if len(ogs) != 1 {
		return ignoreTimeout, fmt.Errorf("found %d operatorGroups, expected 1", len(ogs))
	}

	timeoutStr, ok := ogs[0].GetAnnotations()[BundleUnpackTimeoutAnnotationKey]
	if !ok {
		return ignoreTimeout, nil
	}

	d, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return ignoreTimeout, fmt.Errorf("failed to parse unpack timeout annotation(%s: %s): %w", BundleUnpackTimeoutAnnotationKey, timeoutStr, err)
	}

	return d, nil
}

// OperatorGroupBundleUnpackRetryInterval returns bundle unpack retry interval from annotation if specified.
// If the retry annotation is not set, return retry = 0 which is subsequently ignored. This interval, if > 0,
// determines the minimum interval between recreating a failed unpack job.
func OperatorGroupBundleUnpackRetryInterval(ogLister v1listers.OperatorGroupNamespaceLister) (time.Duration, error) {
	ogs, err := ogLister.List(k8slabels.Everything())
	if err != nil {
		return 0, err
	}
	if len(ogs) != 1 {
		return 0, fmt.Errorf("found %d operatorGroups, expected 1", len(ogs))
	}

	timeoutStr, ok := ogs[0].GetAnnotations()[BundleUnpackRetryMinimumIntervalAnnotationKey]
	if !ok {
		return 0, nil
	}

	d, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return 0, fmt.Errorf("failed to parse unpack retry annotation(%s: %s): %w", BundleUnpackRetryMinimumIntervalAnnotationKey, timeoutStr, err)
	}

	return d, nil
}

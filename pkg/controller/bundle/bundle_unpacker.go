package bundle

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/configmap"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	listersbatchv1 "k8s.io/client-go/listers/batch/v1"
	listerscorev1 "k8s.io/client-go/listers/core/v1"
	listersrbacv1 "k8s.io/client-go/listers/rbac/v1"

	"github.com/operator-framework/api/pkg/operators/reference"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	listersoperatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/projection"
)

const (
	// TODO: Move to operator-framework/api/pkg/operators/v1alpha1
	// BundleLookupFailed describes conditions types for when BundleLookups fail
	BundleLookupFailed operatorsv1alpha1.BundleLookupConditionType = "BundleLookupFailed"

	// TODO: This can be a spec field
	// BundleUnpackTimeoutAnnotationKey allows setting a bundle unpack timeout per InstallPlan
	// and overrides the default specified by the --bundle-unpack-timeout flag
	// The time duration should be in the same format as accepted by time.ParseDuration()
	// e.g 1m30s
	BundleUnpackTimeoutAnnotationKey = "bundle-unpack-timeout"
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
		Spec: batchv1.JobSpec{
			//ttlSecondsAfterFinished: 0 // can use in the future to not have to clean up job
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: cmRef.Name,
				},
				Spec: corev1.PodSpec{
					// With restartPolicy = "OnFailure" when the spec.backoffLimit is reached, the job controller will delete all
					// the job's pods to stop them from crashlooping forever.
					// By setting restartPolicy = "Never" the pods don't get cleaned up since they're not running after a failure.
					// Keeping the pods around after failures helps in inspecting the logs of a failed bundle unpack job.
					// See: https://kubernetes.io/docs/concepts/workloads/controllers/job/#pod-backoff-failure-policy
					RestartPolicy:    corev1.RestartPolicyNever,
					ImagePullSecrets: secrets,
					Containers: []corev1.Container{
						{
							Name:    "extract",
							Image:   c.opmImage,
							Command: []string{"opm", "alpha", "bundle", "extract", "-m", "/bundle/", "-n", cmRef.Namespace, "-c", cmRef.Name},
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
						},
						{
							Name:            "pull",
							Image:           bundlePath,
							ImagePullPolicy: "Always",
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
				},
			},
		},
	}
	job.SetNamespace(cmRef.Namespace)
	job.SetName(cmRef.Name)
	job.SetOwnerReferences([]metav1.OwnerReference{ownerRef(cmRef)})

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
	UnpackBundle(lookup *operatorsv1alpha1.BundleLookup, annotationUnpackTimeout time.Duration) (result *BundleUnpackResult, err error)
}

type ConfigMapUnpacker struct {
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

func (c *ConfigMapUnpacker) UnpackBundle(lookup *operatorsv1alpha1.BundleLookup, annotationUnpackTimeout time.Duration) (result *BundleUnpackResult, err error) {

	result = newBundleUnpackResult(lookup)

	// if bundle lookup failed condition already present, then there is nothing more to do
	failedCond := result.GetCondition(BundleLookupFailed)
	if failedCond.Status == corev1.ConditionTrue {
		return result, nil
	}

	// if pending condition is not true then bundle has already been unpacked(unknown) or failed(false)
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
	job, err = c.ensureJob(cmRef, result.Path, secrets, annotationUnpackTimeout)
	if err != nil {
		return
	}

	// Check if bundle unpack job has failed due a timeout
	// Return a BundleJobError so we can mark the InstallPlan as Failed
	isFailed, jobCond := jobConditionTrue(job, batchv1.JobFailed)
	if isFailed {
		// Add the BundleLookupFailed condition with the message and reason from the job failure
		failedCond.Status = corev1.ConditionTrue
		failedCond.Reason = jobCond.Reason
		failedCond.Message = jobCond.Message
		failedCond.LastTransitionTime = &now
		result.SetCondition(failedCond)

		// BundleLookupPending is false with reason being job failed
		pendingCond.Status = corev1.ConditionFalse
		pendingCond.Reason = JobFailedReason
		pendingCond.Message = JobFailedMessage
		pendingCond.LastTransitionTime = &now
		result.SetCondition(pendingCond)

		return
	}

	if isComplete, _ := jobConditionTrue(job, batchv1.JobComplete); !isComplete {
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
	containerStatusMessages := ""
	// List pods for unpack job
	podLabel := map[string]string{"job-name": job.GetName()}
	pods, listErr := c.podLister.Pods(job.GetNamespace()).List(k8slabels.SelectorFromSet(podLabel))
	if listErr != nil {
		return containerStatusMessages, fmt.Errorf("Failed to list pods for job(%s): %v", job.GetName(), listErr)
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

			// Aggregate the wait reasons for all pending containers
			containerStatusMessages = containerStatusMessages +
				fmt.Sprintf("Unpack pod(%s/%s) container(%s) is pending. Reason: %s, Message: %s | ",
					pod.Namespace, pod.Name, ic.Name, ic.State.Waiting.Reason, ic.State.Waiting.Message)
		}
	}

	return containerStatusMessages, nil
}

func (c *ConfigMapUnpacker) ensureConfigmap(csRef *corev1.ObjectReference, name string) (cm *corev1.ConfigMap, err error) {
	fresh := &corev1.ConfigMap{}
	fresh.SetNamespace(csRef.Namespace)
	fresh.SetName(name)
	fresh.SetOwnerReferences([]metav1.OwnerReference{ownerRef(csRef)})

	cm, err = c.cmLister.ConfigMaps(fresh.GetNamespace()).Get(fresh.GetName())
	if apierrors.IsNotFound(err) {
		cm, err = c.client.CoreV1().ConfigMaps(fresh.GetNamespace()).Create(context.TODO(), fresh, metav1.CreateOptions{})
	}

	return
}

func (c *ConfigMapUnpacker) ensureJob(cmRef *corev1.ObjectReference, bundlePath string, secrets []corev1.LocalObjectReference, annotationUnpackTimeout time.Duration) (job *batchv1.Job, err error) {
	fresh := c.job(cmRef, bundlePath, secrets, annotationUnpackTimeout)
	job, err = c.jobLister.Jobs(fresh.GetNamespace()).Get(fresh.GetName())
	if err != nil {
		if apierrors.IsNotFound(err) {
			job, err = c.client.BatchV1().Jobs(fresh.GetNamespace()).Create(context.TODO(), fresh, metav1.CreateOptions{})
		}

		return
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

	role, err = c.roleLister.Roles(fresh.GetNamespace()).Get(fresh.GetName())
	if err != nil {
		if apierrors.IsNotFound(err) {
			role, err = c.client.RbacV1().Roles(fresh.GetNamespace()).Create(context.TODO(), fresh, metav1.CreateOptions{})
		}

		return
	}

	// Add the policy rule if necessary
	for _, existing := range role.Rules {
		if equality.Semantic.DeepDerivative(rule, existing) {
			return
		}
	}
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

	roleBinding, err = c.rbLister.RoleBindings(fresh.GetNamespace()).Get(fresh.GetName())
	if err != nil {
		if apierrors.IsNotFound(err) {
			roleBinding, err = c.client.RbacV1().RoleBindings(fresh.GetNamespace()).Create(context.TODO(), fresh, metav1.CreateOptions{})
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

// jobConditionTrue returns true if the given job has the given condition with the given condition type true, and returns false otherwise.
// Also returns the condition if true
func jobConditionTrue(job *batchv1.Job, conditionType batchv1.JobConditionType) (bool, *batchv1.JobCondition) {
	if job == nil {
		return false, nil
	}

	for _, cond := range job.Status.Conditions {
		if cond.Type == conditionType && cond.Status == corev1.ConditionTrue {
			return true, &cond
		}
	}
	return false, nil
}

package bundle

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/configmap"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	listersbatchv1 "k8s.io/client-go/listers/batch/v1"
	listerscorev1 "k8s.io/client-go/listers/core/v1"
	listersrbacv1 "k8s.io/client-go/listers/rbac/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/reference"
	operatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	listersoperatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
)

type BundleUnpackResult struct {
	*operatorsv1alpha1.BundleLookup

	bundle *api.Bundle
	name   string
}

func (b *BundleUnpackResult) Bundle() *api.Bundle {
	return b.bundle
}

var catalogSourceGVK = operatorsv1alpha1.SchemeGroupVersion.WithKind(operatorsv1alpha1.CatalogSourceKind)

func newBundleUnpackResult(lookup *operatorsv1alpha1.BundleLookup) *BundleUnpackResult {
	return &BundleUnpackResult{
		BundleLookup: lookup.DeepCopy(),
		name:         hash(lookup.Path),
	}
}

func (c *ConfigMapUnpacker) job(cmRef *corev1.ObjectReference, bundlePath string) *batchv1.Job {
	job := &batchv1.Job{
		Spec: batchv1.JobSpec{
			//ttlSecondsAfterFinished: 0 // can use in the future to not have to clean up job
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: cmRef.Name,
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyOnFailure,
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
						},
						{
							Name:    "pull",
							Image:   bundlePath,
							Command: []string{"/util/cpb", "/bundle"}, // Copy bundle content to its mount
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

	return job
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . Unpacker

type Unpacker interface {
	UnpackBundle(lookup *operatorsv1alpha1.BundleLookup) (result *BundleUnpackResult, err error)
}

type ConfigMapUnpacker struct {
	opmImage   string
	utilImage  string
	client     kubernetes.Interface
	csLister   listersoperatorsv1alpha1.CatalogSourceLister
	cmLister   listerscorev1.ConfigMapLister
	jobLister  listersbatchv1.JobLister
	roleLister listersrbacv1.RoleLister
	rbLister   listersrbacv1.RoleBindingLister
	loader     *configmap.BundleLoader
	now        func() metav1.Time
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
	JobIncompleteReason         = "JobIncomplete"
	JobIncompleteMessage        = "unpack job not completed"
)

func (c *ConfigMapUnpacker) UnpackBundle(lookup *operatorsv1alpha1.BundleLookup) (result *BundleUnpackResult, err error) {
	result = newBundleUnpackResult(lookup)
	cond := result.GetCondition(operatorsv1alpha1.BundleLookupPending)
	now := c.now()

	var cs *operatorsv1alpha1.CatalogSource
	if cs, err = c.csLister.CatalogSources(result.CatalogSourceRef.Namespace).Get(result.CatalogSourceRef.Name); err != nil {
		if apierrors.IsNotFound(err) && cond.Status != corev1.ConditionTrue && cond.Reason != CatalogSourceMissingReason {
			cond.Status = corev1.ConditionTrue
			cond.Reason = CatalogSourceMissingReason
			cond.Message = CatalogSourceMissingMessage
			cond.LastTransitionTime = &now
			result.SetCondition(cond)
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

	var job *batchv1.Job
	job, err = c.ensureJob(cmRef, result.Path)
	if err != nil {
		return
	}

	if !jobConditionTrue(job, batchv1.JobComplete) && cond.Status != corev1.ConditionTrue && cond.Reason != JobIncompleteReason {
		cond.Status = corev1.ConditionTrue
		cond.Reason = JobIncompleteReason
		cond.Message = JobIncompleteMessage
		cond.LastTransitionTime = &now
		result.SetCondition(cond)

		return
	}

	result.bundle, err = c.loader.Load(cm)
	if err != nil {
		return
	}

	// A successful load should remove the pending condition
	result.RemoveCondition(operatorsv1alpha1.BundleLookupPending)

	return
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

func (c *ConfigMapUnpacker) ensureJob(cmRef *corev1.ObjectReference, bundlePath string) (job *batchv1.Job, err error) {
	fresh := c.job(cmRef, bundlePath)
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
func jobConditionTrue(job *batchv1.Job, conditionType batchv1.JobConditionType) bool {
	if job == nil {
		return false
	}

	for _, cond := range job.Status.Conditions {
		if cond.Type == conditionType && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

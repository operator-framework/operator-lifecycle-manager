package bundle

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	crfake "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	crinformers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	v1listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/configmap"
)

const (
	csvJSON     = "{\"apiVersion\":\"operators.coreos.com/v1alpha1\",\"kind\":\"ClusterServiceVersion\",\"metadata\":{\"annotations\":{\"olm.skipRange\":\"\\u003c 0.6.0\",\"tectonic-visibility\":\"ocs\"},\"name\":\"etcdoperator.v0.9.2\",\"namespace\":\"placeholder\"},\"spec\":{\"customresourcedefinitions\":{\"owned\":[{\"description\":\"Represents a cluster of etcd nodes.\",\"displayName\":\"etcd Cluster\",\"kind\":\"EtcdCluster\",\"name\":\"etcdclusters.etcd.database.coreos.com\",\"resources\":[{\"kind\":\"Service\",\"version\":\"v1\"},{\"kind\":\"Pod\",\"version\":\"v1\"}],\"specDescriptors\":[{\"description\":\"The desired number of member Pods for the etcd cluster.\",\"displayName\":\"Size\",\"path\":\"size\",\"x-descriptors\":[\"urn:alm:descriptor:com.tectonic.ui:podCount\"]},{\"description\":\"Limits describes the minimum/maximum amount of compute resources required/allowed\",\"displayName\":\"Resource Requirements\",\"path\":\"pod.resources\",\"x-descriptors\":[\"urn:alm:descriptor:com.tectonic.ui:resourceRequirements\"]}],\"statusDescriptors\":[{\"description\":\"The status of each of the member Pods for the etcd cluster.\",\"displayName\":\"Member Status\",\"path\":\"members\",\"x-descriptors\":[\"urn:alm:descriptor:com.tectonic.ui:podStatuses\"]},{\"description\":\"The service at which the running etcd cluster can be accessed.\",\"displayName\":\"Service\",\"path\":\"serviceName\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes:Service\"]},{\"description\":\"The current size of the etcd cluster.\",\"displayName\":\"Cluster Size\",\"path\":\"size\"},{\"description\":\"The current version of the etcd cluster.\",\"displayName\":\"Current Version\",\"path\":\"currentVersion\"},{\"description\":\"The target version of the etcd cluster, after upgrading.\",\"displayName\":\"Target Version\",\"path\":\"targetVersion\"},{\"description\":\"The current status of the etcd cluster.\",\"displayName\":\"Status\",\"path\":\"phase\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes.phase\"]},{\"description\":\"Explanation for the current status of the cluster.\",\"displayName\":\"Status Details\",\"path\":\"reason\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes.phase:reason\"]}],\"version\":\"v1beta2\"},{\"description\":\"Represents the intent to backup an etcd cluster.\",\"displayName\":\"etcd Backup\",\"kind\":\"EtcdBackup\",\"name\":\"etcdbackups.etcd.database.coreos.com\",\"specDescriptors\":[{\"description\":\"Specifies the endpoints of an etcd cluster.\",\"displayName\":\"etcd Endpoint(s)\",\"path\":\"etcdEndpoints\",\"x-descriptors\":[\"urn:alm:descriptor:etcd:endpoint\"]},{\"description\":\"The full AWS S3 path where the backup is saved.\",\"displayName\":\"S3 Path\",\"path\":\"s3.path\",\"x-descriptors\":[\"urn:alm:descriptor:aws:s3:path\"]},{\"description\":\"The name of the secret object that stores the AWS credential and config files.\",\"displayName\":\"AWS Secret\",\"path\":\"s3.awsSecret\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes:Secret\"]}],\"statusDescriptors\":[{\"description\":\"Indicates if the backup was successful.\",\"displayName\":\"Succeeded\",\"path\":\"succeeded\",\"x-descriptors\":[\"urn:alm:descriptor:text\"]},{\"description\":\"Indicates the reason for any backup related failures.\",\"displayName\":\"Reason\",\"path\":\"reason\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes.phase:reason\"]}],\"version\":\"v1beta2\"},{\"description\":\"Represents the intent to restore an etcd cluster from a backup.\",\"displayName\":\"etcd Restore\",\"kind\":\"EtcdRestore\",\"name\":\"etcdrestores.etcd.database.coreos.com\",\"specDescriptors\":[{\"description\":\"References the EtcdCluster which should be restored,\",\"displayName\":\"etcd Cluster\",\"path\":\"etcdCluster.name\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes:EtcdCluster\",\"urn:alm:descriptor:text\"]},{\"description\":\"The full AWS S3 path where the backup is saved.\",\"displayName\":\"S3 Path\",\"path\":\"s3.path\",\"x-descriptors\":[\"urn:alm:descriptor:aws:s3:path\"]},{\"description\":\"The name of the secret object that stores the AWS credential and config files.\",\"displayName\":\"AWS Secret\",\"path\":\"s3.awsSecret\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes:Secret\"]}],\"statusDescriptors\":[{\"description\":\"Indicates if the restore was successful.\",\"displayName\":\"Succeeded\",\"path\":\"succeeded\",\"x-descriptors\":[\"urn:alm:descriptor:text\"]},{\"description\":\"Indicates the reason for any restore related failures.\",\"displayName\":\"Reason\",\"path\":\"reason\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes.phase:reason\"]}],\"version\":\"v1beta2\"}],\"required\":[{\"description\":\"Represents a cluster of etcd nodes.\",\"displayName\":\"etcd Cluster\",\"kind\":\"EtcdCluster\",\"name\":\"etcdclusters.etcd.database.coreos.com\",\"resources\":[{\"kind\":\"Service\",\"version\":\"v1\"},{\"kind\":\"Pod\",\"version\":\"v1\"}],\"specDescriptors\":[{\"description\":\"The desired number of member Pods for the etcd cluster.\",\"displayName\":\"Size\",\"path\":\"size\",\"x-descriptors\":[\"urn:alm:descriptor:com.tectonic.ui:podCount\"]}],\"version\":\"v1beta2\"}]},\"description\":\"etcd is a distributed key value store that provides a reliable way to store data across a cluster of machines. It’s open-source and available on GitHub. etcd gracefully handles leader elections during network partitions and will tolerate machine failure, including the leader. Your applications can read and write data into etcd.\\nA simple use-case is to store database connection details or feature flags within etcd as key value pairs. These values can be watched, allowing your app to reconfigure itself when they change. Advanced uses take advantage of the consistency guarantees to implement database leader elections or do distributed locking across a cluster of workers.\\n\\n_The etcd Open Cloud Service is Public Alpha. The goal before Beta is to fully implement backup features._\\n\\n### Reading and writing to etcd\\n\\nCommunicate with etcd though its command line utility `etcdctl` or with the API using the automatically generated Kubernetes Service.\\n\\n[Read the complete guide to using the etcd Open Cloud Service](https://coreos.com/tectonic/docs/latest/alm/etcd-ocs.html)\\n\\n### Supported Features\\n\\n\\n**High availability**\\n\\n\\nMultiple instances of etcd are networked together and secured. Individual failures or networking issues are transparently handled to keep your cluster up and running.\\n\\n\\n**Automated updates**\\n\\n\\nRolling out a new etcd version works like all Kubernetes rolling updates. Simply declare the desired version, and the etcd service starts a safe rolling update to the new version automatically.\\n\\n\\n**Backups included**\\n\\n\\nComing soon, the ability to schedule backups to happen on or off cluster.\\n\",\"displayName\":\"etcd\",\"install\":{\"spec\":{\"deployments\":[{\"name\":\"etcd-operator\",\"spec\":{\"replicas\":1,\"selector\":{\"matchLabels\":{\"name\":\"etcd-operator-alm-owned\"}},\"template\":{\"metadata\":{\"labels\":{\"name\":\"etcd-operator-alm-owned\"},\"name\":\"etcd-operator-alm-owned\"},\"spec\":{\"containers\":[{\"command\":[\"etcd-operator\",\"--create-crd=false\"],\"env\":[{\"name\":\"MY_POD_NAMESPACE\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.namespace\"}}},{\"name\":\"MY_POD_NAME\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.name\"}}}],\"image\":\"quay.io/coreos/etcd-operator@sha256:c0301e4686c3ed4206e370b42de5a3bd2229b9fb4906cf85f3f30650424abec2\",\"name\":\"etcd-operator\"},{\"command\":[\"etcd-backup-operator\",\"--create-crd=false\"],\"env\":[{\"name\":\"MY_POD_NAMESPACE\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.namespace\"}}},{\"name\":\"MY_POD_NAME\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.name\"}}}],\"image\":\"quay.io/coreos/etcd-operator@sha256:c0301e4686c3ed4206e370b42de5a3bd2229b9fb4906cf85f3f30650424abec2\",\"name\":\"etcd-backup-operator\"},{\"command\":[\"etcd-restore-operator\",\"--create-crd=false\"],\"env\":[{\"name\":\"MY_POD_NAMESPACE\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.namespace\"}}},{\"name\":\"MY_POD_NAME\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.name\"}}}],\"image\":\"quay.io/coreos/etcd-operator@sha256:c0301e4686c3ed4206e370b42de5a3bd2229b9fb4906cf85f3f30650424abec2\",\"name\":\"etcd-restore-operator\"}],\"serviceAccountName\":\"etcd-operator\"}}}}],\"permissions\":[{\"rules\":[{\"apiGroups\":[\"etcd.database.coreos.com\"],\"resources\":[\"etcdclusters\",\"etcdbackups\",\"etcdrestores\"],\"verbs\":[\"*\"]},{\"apiGroups\":[\"\"],\"resources\":[\"pods\",\"services\",\"endpoints\",\"persistentvolumeclaims\",\"events\"],\"verbs\":[\"*\"]},{\"apiGroups\":[\"apps\"],\"resources\":[\"deployments\"],\"verbs\":[\"*\"]},{\"apiGroups\":[\"\"],\"resources\":[\"secrets\"],\"verbs\":[\"get\"]}],\"serviceAccountName\":\"etcd-operator\"}]},\"strategy\":\"deployment\"},\"keywords\":[\"etcd\",\"key value\",\"database\",\"coreos\",\"open source\"],\"labels\":{\"alm-owner-etcd\":\"etcdoperator\",\"operated-by\":\"etcdoperator\"},\"links\":[{\"name\":\"Blog\",\"url\":\"https://coreos.com/etcd\"},{\"name\":\"Documentation\",\"url\":\"https://coreos.com/operators/etcd/docs/latest/\"},{\"name\":\"etcd Operator Source Code\",\"url\":\"https://github.com/coreos/etcd-operator\"}],\"maintainers\":[{\"email\":\"support@coreos.com\",\"name\":\"CoreOS, Inc\"}],\"maturity\":\"alpha\",\"provider\":{\"name\":\"CoreOS, Inc\"},\"relatedImages\":[{\"image\":\"quay.io/coreos/etcd@sha256:3816b6daf9b66d6ced6f0f966314e2d4f894982c6b1493061502f8c2bf86ac84\",\"name\":\"etcd-v3.4.0\"},{\"image\":\"quay.io/coreos/etcd@sha256:49d3d4a81e0d030d3f689e7167f23e120abf955f7d08dbedf3ea246485acee9f\",\"name\":\"etcd-3.4.1\"}],\"replaces\":\"etcdoperator.v0.9.0\",\"selector\":{\"matchLabels\":{\"alm-owner-etcd\":\"etcdoperator\",\"operated-by\":\"etcdoperator\"}},\"skips\":[\"etcdoperator.v0.9.1\"],\"version\":\"0.9.2\"}}"
	etcdBackup  = "{\"apiVersion\":\"apiextensions.k8s.io/v1beta1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"name\":\"etcdbackups.etcd.database.coreos.com\"},\"spec\":{\"group\":\"etcd.database.coreos.com\",\"names\":{\"kind\":\"EtcdBackup\",\"listKind\":\"EtcdBackupList\",\"plural\":\"etcdbackups\",\"singular\":\"etcdbackup\"},\"scope\":\"Namespaced\",\"version\":\"v1beta2\"}}"
	etcdCluster = "{\"apiVersion\":\"apiextensions.k8s.io/v1beta1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"name\":\"etcdclusters.etcd.database.coreos.com\"},\"spec\":{\"group\":\"etcd.database.coreos.com\",\"names\":{\"kind\":\"EtcdCluster\",\"listKind\":\"EtcdClusterList\",\"plural\":\"etcdclusters\",\"shortNames\":[\"etcdclus\",\"etcd\"],\"singular\":\"etcdcluster\"},\"scope\":\"Namespaced\",\"version\":\"v1beta2\"}}"
	etcdRestore = "{\"apiVersion\":\"apiextensions.k8s.io/v1beta1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"name\":\"etcdrestores.etcd.database.coreos.com\"},\"spec\":{\"group\":\"etcd.database.coreos.com\",\"names\":{\"kind\":\"EtcdRestore\",\"listKind\":\"EtcdRestoreList\",\"plural\":\"etcdrestores\",\"singular\":\"etcdrestore\"},\"scope\":\"Namespaced\",\"version\":\"v1beta2\"}}"
	opmImage    = "opm-image"
	utilImage   = "util-image"
	bundlePath  = "bundle-path"
	digestPath  = "bundle-path@sha256:54d626e08c1c802b305dad30b7e54a82f102390cc92c7d4db112048935236e9c"
	runAsUser   = 1001
)

func TestConfigMapUnpacker(t *testing.T) {
	pathHash := hash(bundlePath)
	digestHash := hash(digestPath)
	start := metav1.Now()
	now := func() metav1.Time {
		return start
	}
	backoffLimit := int32(3)
	// Used to set the default value for job.spec.ActiveDeadlineSeconds
	// that would normally be passed from the cmdline flag
	defaultUnpackDuration := 10 * time.Minute
	defaultUnpackTimeoutSeconds := int64(defaultUnpackDuration.Seconds())

	// Custom timeout to override the default cmdline flag ActiveDeadlineSeconds value
	customAnnotationDuration := 2 * time.Minute
	customAnnotationTimeoutSeconds := int64(customAnnotationDuration.Seconds())

	podTolerations := []corev1.Toleration{
		// arch-specific tolerations
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
		// control-plane-specific tolerations
		{
			Key:      "node-role.kubernetes.io/master",
			Operator: "Exists",
			Effect:   "NoSchedule",
		},
		{
			Key:               "node.kubernetes.io/unreachable",
			Operator:          "Exists",
			Effect:            "NoExecute",
			TolerationSeconds: ptr.To[int64](120),
		},
		{
			Key:               "node.kubernetes.io/not-ready",
			Operator:          "Exists",
			Effect:            "NoExecute",
			TolerationSeconds: ptr.To[int64](120),
		},
	}

	type fields struct {
		objs []runtime.Object
		crs  []runtime.Object
	}
	type args struct {
		lookup *operatorsv1alpha1.BundleLookup
		// A negative timeout duration arg means it will be ignored and the default flag timeout will be used
		annotationTimeout time.Duration
	}
	type expected struct {
		res          *BundleUnpackResult
		err          error
		configMaps   []*corev1.ConfigMap
		jobs         []*batchv1.Job
		roles        []*rbacv1.Role
		roleBindings []*rbacv1.RoleBinding
	}

	tests := []struct {
		description string
		fields      fields
		args        args
		expected    expected
	}{
		{
			description: "NoCatalogSource/NoConfigMap/NoJob/NotCreated/Pending",
			fields:      fields{},
			args: args{
				annotationTimeout: -1 * time.Minute,
				lookup: &operatorsv1alpha1.BundleLookup{
					Path:     bundlePath,
					Replaces: "",
					CatalogSourceRef: &corev1.ObjectReference{
						Namespace: "ns-a",
						Name:      "src-a",
					},
					Conditions: []operatorsv1alpha1.BundleLookupCondition{
						{
							Type:    operatorsv1alpha1.BundleLookupPending,
							Status:  corev1.ConditionTrue,
							Reason:  JobNotStartedReason,
							Message: JobNotStartedMessage,
						},
					},
				},
			},
			expected: expected{
				res: &BundleUnpackResult{
					BundleLookup: &operatorsv1alpha1.BundleLookup{
						Path:     bundlePath,
						Replaces: "",
						CatalogSourceRef: &corev1.ObjectReference{
							Namespace: "ns-a",
							Name:      "src-a",
						},
						Conditions: []operatorsv1alpha1.BundleLookupCondition{
							{
								Type:               operatorsv1alpha1.BundleLookupPending,
								Status:             corev1.ConditionTrue,
								Reason:             CatalogSourceMissingReason,
								Message:            CatalogSourceMissingMessage,
								LastTransitionTime: &start,
							},
						},
					},
					name: pathHash,
				},
			},
		},
		{
			description: "CatalogSourcePresent/NoConfigMap/NoJob/JobCreated/Pending/WithCustomTimeout",
			fields: fields{
				crs: []runtime.Object{
					&operatorsv1alpha1.CatalogSource{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "ns-a",
							Name:      "src-a",
						},
						Spec: operatorsv1alpha1.CatalogSourceSpec{
							Secrets: []string{"my-secret"},
						},
					},
				},
			},
			args: args{
				// We override the default timeout and expect to see the job created with
				// the custom annotation based timeout value
				annotationTimeout: customAnnotationDuration,
				lookup: &operatorsv1alpha1.BundleLookup{
					Path:     bundlePath,
					Replaces: "",
					CatalogSourceRef: &corev1.ObjectReference{
						Namespace: "ns-a",
						Name:      "src-a",
					},
					Conditions: []operatorsv1alpha1.BundleLookupCondition{
						{
							Type:    operatorsv1alpha1.BundleLookupPending,
							Status:  corev1.ConditionTrue,
							Reason:  JobNotStartedReason,
							Message: JobNotStartedMessage,
						},
					},
				},
			},
			expected: expected{
				res: &BundleUnpackResult{
					BundleLookup: &operatorsv1alpha1.BundleLookup{
						Path:     bundlePath,
						Replaces: "",
						CatalogSourceRef: &corev1.ObjectReference{
							Namespace: "ns-a",
							Name:      "src-a",
						},
						Conditions: []operatorsv1alpha1.BundleLookupCondition{
							{
								Type:               operatorsv1alpha1.BundleLookupPending,
								Status:             corev1.ConditionTrue,
								Reason:             JobIncompleteReason,
								Message:            JobIncompleteMessage,
								LastTransitionTime: &start,
							},
						},
					},
					name: pathHash,
				},
				configMaps: []*corev1.ConfigMap{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      pathHash,
							Namespace: "ns-a",
							Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion:         "operators.coreos.com/v1alpha1",
									Kind:               "CatalogSource",
									Name:               "src-a",
									Controller:         &blockOwnerDeletion,
									BlockOwnerDeletion: &blockOwnerDeletion,
								},
							},
						},
					},
				},
				jobs: []*batchv1.Job{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      pathHash,
							Namespace: "ns-a",
							Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue, BundleUnpackRefLabel: pathHash},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion:         "v1",
									Kind:               "ConfigMap",
									Name:               pathHash,
									Controller:         &blockOwnerDeletion,
									BlockOwnerDeletion: &blockOwnerDeletion,
								},
							},
						},
						Spec: batchv1.JobSpec{
							// The expected job's timeout should be set to the custom annotation timeout
							ActiveDeadlineSeconds: &customAnnotationTimeoutSeconds,
							BackoffLimit:          &backoffLimit,
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{
									Name: pathHash,
									Labels: map[string]string{
										install.OLMManagedLabelKey: install.OLMManagedLabelValue,
										BundleUnpackRefLabel:       pathHash,
									},
								},
								Spec: corev1.PodSpec{
									RestartPolicy:    corev1.RestartPolicyNever,
									ImagePullSecrets: []corev1.LocalObjectReference{{Name: "my-secret"}},
									SecurityContext: &corev1.PodSecurityContext{
										RunAsNonRoot: ptr.To(bool(true)),
										RunAsUser:    ptr.To(int64(runAsUser)),
										SeccompProfile: &corev1.SeccompProfile{
											Type: corev1.SeccompProfileTypeRuntimeDefault,
										},
									},
									Containers: []corev1.Container{
										{
											Name:    "extract",
											Image:   opmImage,
											Command: []string{"opm", "alpha", "bundle", "extract", "-m", "/bundle/", "-n", "ns-a", "-c", pathHash, "-z"},
											Env: []corev1.EnvVar{
												{
													Name:  configmap.EnvContainerImage,
													Value: bundlePath,
												},
											},
											VolumeMounts: []corev1.VolumeMount{
												{
													Name:      "bundle",
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
												ReadOnlyRootFilesystem:   ptr.To(true),
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
											Image:   utilImage,
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
												ReadOnlyRootFilesystem:   ptr.To(true),
												Capabilities: &corev1.Capabilities{
													Drop: []corev1.Capability{"ALL"},
												},
											},
											TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
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
											SecurityContext: &corev1.SecurityContext{
												AllowPrivilegeEscalation: ptr.To(bool(false)),
												ReadOnlyRootFilesystem:   ptr.To(true),
												Capabilities: &corev1.Capabilities{
													Drop: []corev1.Capability{"ALL"},
												},
											},
											TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
										},
									},
									Volumes: []corev1.Volume{
										{
											Name: "bundle",
											VolumeSource: corev1.VolumeSource{
												EmptyDir: &corev1.EmptyDirVolumeSource{},
											},
										},
										{
											Name: "util",
											VolumeSource: corev1.VolumeSource{
												EmptyDir: &corev1.EmptyDirVolumeSource{},
											},
										},
									},
									NodeSelector: map[string]string{
										"kubernetes.io/os": "linux",
									},
									Tolerations: podTolerations,
								},
							},
						},
					},
				},
				roles: []*rbacv1.Role{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      pathHash,
							Namespace: "ns-a",
							Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion:         "v1",
									Kind:               "ConfigMap",
									Name:               pathHash,
									Controller:         &blockOwnerDeletion,
									BlockOwnerDeletion: &blockOwnerDeletion,
								},
							},
						},
						Rules: []rbacv1.PolicyRule{
							{
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
									pathHash,
								},
							},
						},
					},
				},
				roleBindings: []*rbacv1.RoleBinding{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      pathHash,
							Namespace: "ns-a",
							Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion:         "v1",
									Kind:               "ConfigMap",
									Name:               pathHash,
									Controller:         &blockOwnerDeletion,
									BlockOwnerDeletion: &blockOwnerDeletion,
								},
							},
						},
						Subjects: []rbacv1.Subject{
							{
								Kind:      "ServiceAccount",
								APIGroup:  "",
								Name:      "default",
								Namespace: "ns-a",
							},
						},
						RoleRef: rbacv1.RoleRef{
							APIGroup: "rbac.authorization.k8s.io",
							Kind:     "Role",
							Name:     pathHash,
						},
					},
				},
			},
		},
		{
			description: "CatalogSourcePresent/ConfigMapPresent/JobPresent/DigestImage/Unpacked",
			fields: fields{
				objs: []runtime.Object{
					&batchv1.Job{
						ObjectMeta: metav1.ObjectMeta{
							Name:      digestHash,
							Namespace: "ns-a",
							Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue, BundleUnpackRefLabel: digestHash},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion:         "v1",
									Kind:               "ConfigMap",
									Name:               digestHash,
									Controller:         &blockOwnerDeletion,
									BlockOwnerDeletion: &blockOwnerDeletion,
								},
							},
						},
						Spec: batchv1.JobSpec{
							ActiveDeadlineSeconds: &defaultUnpackTimeoutSeconds,
							BackoffLimit:          &backoffLimit,
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{
									Name: digestHash,
									Labels: map[string]string{
										install.OLMManagedLabelKey: install.OLMManagedLabelValue,
										BundleUnpackRefLabel:       digestHash,
									},
								},
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									SecurityContext: &corev1.PodSecurityContext{
										RunAsNonRoot: ptr.To(bool(true)),
										RunAsUser:    ptr.To(int64(runAsUser)),
										SeccompProfile: &corev1.SeccompProfile{
											Type: corev1.SeccompProfileTypeRuntimeDefault,
										},
									},
									Containers: []corev1.Container{
										{
											Name:    "extract",
											Image:   opmImage,
											Command: []string{"opm", "alpha", "bundle", "extract", "-m", "/bundle/", "-n", "ns-a", "-c", digestHash, "-z"},
											Env: []corev1.EnvVar{
												{
													Name:  configmap.EnvContainerImage,
													Value: digestPath,
												},
											},
											VolumeMounts: []corev1.VolumeMount{
												{
													Name:      "bundle",
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
												ReadOnlyRootFilesystem:   ptr.To(true),
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
											Image:   utilImage,
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
												ReadOnlyRootFilesystem:   ptr.To(true),
												Capabilities: &corev1.Capabilities{
													Drop: []corev1.Capability{"ALL"},
												},
											},
											TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
										},
										{
											Name:            "pull",
											Image:           digestPath,
											ImagePullPolicy: "IfNotPresent",
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
												ReadOnlyRootFilesystem:   ptr.To(true),
												Capabilities: &corev1.Capabilities{
													Drop: []corev1.Capability{"ALL"},
												},
											},
											TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
										},
									},
									Volumes: []corev1.Volume{
										{
											Name: "bundle",
											VolumeSource: corev1.VolumeSource{
												EmptyDir: &corev1.EmptyDirVolumeSource{},
											},
										},
										{
											Name: "util",
											VolumeSource: corev1.VolumeSource{
												EmptyDir: &corev1.EmptyDirVolumeSource{},
											},
										},
									},
									NodeSelector: map[string]string{
										"kubernetes.io/os": "linux",
									},
									Tolerations: podTolerations,
								},
							},
						},
						Status: batchv1.JobStatus{
							Succeeded:      1,
							StartTime:      &start,
							CompletionTime: &start,
							Conditions: []batchv1.JobCondition{
								{
									LastProbeTime:      start,
									LastTransitionTime: start,
									Status:             corev1.ConditionTrue,
									Type:               batchv1.JobComplete,
								},
							},
						},
					},
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      digestHash,
							Namespace: "ns-a",
							Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion:         "operators.coreos.com/v1alpha1",
									Kind:               "CatalogSource",
									Name:               "src-a",
									Controller:         &blockOwnerDeletion,
									BlockOwnerDeletion: &blockOwnerDeletion,
								},
							},
						},
						Data: map[string]string{
							"etcdbackups.crd.json":  etcdBackup,
							"etcdclusters.crd.json": etcdCluster,
							"csv.json":              csvJSON,
							"etcdrestores.crd.json": etcdRestore,
						},
					},
				},
				crs: []runtime.Object{
					&operatorsv1alpha1.CatalogSource{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "ns-a",
							Name:      "src-a",
						},
					},
				},
			},
			args: args{
				annotationTimeout: -1 * time.Minute,
				lookup: &operatorsv1alpha1.BundleLookup{
					Path:     digestPath,
					Replaces: "",
					CatalogSourceRef: &corev1.ObjectReference{
						Namespace: "ns-a",
						Name:      "src-a",
					},
					Conditions: []operatorsv1alpha1.BundleLookupCondition{
						{
							Type:               operatorsv1alpha1.BundleLookupPending,
							Status:             corev1.ConditionTrue,
							Reason:             JobIncompleteReason,
							Message:            JobIncompleteMessage,
							LastTransitionTime: &start,
						},
					},
				},
			},
			expected: expected{
				res: &BundleUnpackResult{
					BundleLookup: &operatorsv1alpha1.BundleLookup{
						Path:     digestPath,
						Replaces: "",
						CatalogSourceRef: &corev1.ObjectReference{
							Namespace: "ns-a",
							Name:      "src-a",
						},
					},
					name: digestHash,
					bundle: &api.Bundle{
						CsvName: "etcdoperator.v0.9.2",
						CsvJson: csvJSON + "\n",
						Object: []string{
							etcdBackup,
							etcdCluster,
							csvJSON,
							etcdRestore,
						},
					},
				},
				configMaps: []*corev1.ConfigMap{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      digestHash,
							Namespace: "ns-a",
							Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion:         "operators.coreos.com/v1alpha1",
									Kind:               "CatalogSource",
									Name:               "src-a",
									Controller:         &blockOwnerDeletion,
									BlockOwnerDeletion: &blockOwnerDeletion,
								},
							},
						},
						Data: map[string]string{
							"etcdbackups.crd.json":  etcdBackup,
							"etcdclusters.crd.json": etcdCluster,
							"csv.json":              csvJSON,
							"etcdrestores.crd.json": etcdRestore,
						},
					},
				},
				jobs: []*batchv1.Job{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      digestHash,
							Namespace: "ns-a",
							Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue, BundleUnpackRefLabel: digestHash},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion:         "v1",
									Kind:               "ConfigMap",
									Name:               digestHash,
									Controller:         &blockOwnerDeletion,
									BlockOwnerDeletion: &blockOwnerDeletion,
								},
							},
						},
						Spec: batchv1.JobSpec{
							ActiveDeadlineSeconds: &defaultUnpackTimeoutSeconds,
							BackoffLimit:          &backoffLimit,
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{
									Name: digestHash,
									Labels: map[string]string{
										install.OLMManagedLabelKey: install.OLMManagedLabelValue,
										BundleUnpackRefLabel:       digestHash,
									},
								},
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									SecurityContext: &corev1.PodSecurityContext{
										RunAsNonRoot: ptr.To(bool(true)),
										RunAsUser:    ptr.To(int64(runAsUser)),
										SeccompProfile: &corev1.SeccompProfile{
											Type: corev1.SeccompProfileTypeRuntimeDefault,
										},
									},
									Containers: []corev1.Container{
										{
											Name:    "extract",
											Image:   opmImage,
											Command: []string{"opm", "alpha", "bundle", "extract", "-m", "/bundle/", "-n", "ns-a", "-c", digestHash, "-z"},
											Env: []corev1.EnvVar{
												{
													Name:  configmap.EnvContainerImage,
													Value: digestPath,
												},
											},
											VolumeMounts: []corev1.VolumeMount{
												{
													Name:      "bundle",
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
												ReadOnlyRootFilesystem:   ptr.To(true),
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
											Image:   utilImage,
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
												ReadOnlyRootFilesystem:   ptr.To(true),
												Capabilities: &corev1.Capabilities{
													Drop: []corev1.Capability{"ALL"},
												},
											},
											TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
										},
										{
											Name:            "pull",
											Image:           digestPath,
											ImagePullPolicy: "IfNotPresent",
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
												ReadOnlyRootFilesystem:   ptr.To(true),
												Capabilities: &corev1.Capabilities{
													Drop: []corev1.Capability{"ALL"},
												},
											},
											TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
										},
									},
									Volumes: []corev1.Volume{
										{
											Name: "bundle",
											VolumeSource: corev1.VolumeSource{
												EmptyDir: &corev1.EmptyDirVolumeSource{},
											},
										},
										{
											Name: "util",
											VolumeSource: corev1.VolumeSource{
												EmptyDir: &corev1.EmptyDirVolumeSource{},
											},
										},
									},
									NodeSelector: map[string]string{
										"kubernetes.io/os": "linux",
									},
									Tolerations: podTolerations,
								},
							},
						},
						Status: batchv1.JobStatus{
							Succeeded:      1,
							StartTime:      &start,
							CompletionTime: &start,
							Conditions: []batchv1.JobCondition{
								{
									LastProbeTime:      start,
									LastTransitionTime: start,
									Status:             corev1.ConditionTrue,
									Type:               batchv1.JobComplete,
								},
							},
						},
					},
				},
				roles: []*rbacv1.Role{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      digestHash,
							Namespace: "ns-a",
							Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion:         "v1",
									Kind:               "ConfigMap",
									Name:               digestHash,
									Controller:         &blockOwnerDeletion,
									BlockOwnerDeletion: &blockOwnerDeletion,
								},
							},
						},
						Rules: []rbacv1.PolicyRule{
							{
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
									digestHash,
								},
							},
						},
					},
				},
				roleBindings: []*rbacv1.RoleBinding{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      digestHash,
							Namespace: "ns-a",
							Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion:         "v1",
									Kind:               "ConfigMap",
									Name:               digestHash,
									Controller:         &blockOwnerDeletion,
									BlockOwnerDeletion: &blockOwnerDeletion,
								},
							},
						},
						Subjects: []rbacv1.Subject{
							{
								Kind:      "ServiceAccount",
								APIGroup:  "",
								Name:      "default",
								Namespace: "ns-a",
							},
						},
						RoleRef: rbacv1.RoleRef{
							APIGroup: "rbac.authorization.k8s.io",
							Kind:     "Role",
							Name:     digestHash,
						},
					},
				},
			},
		},
		{
			description: "CatalogSourcePresent/JobPending/PodPending/BundlePending/WithContainerStatus",
			fields: fields{
				objs: []runtime.Object{
					&corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      pathHash + "-pod",
							Namespace: "ns-a",
							Labels:    map[string]string{"job-name": pathHash},
						},
						Status: corev1.PodStatus{
							Phase: corev1.PodPending,
							InitContainerStatuses: []corev1.ContainerStatus{
								{
									Name:  "pull",
									Ready: false,
									State: corev1.ContainerState{
										Waiting: &corev1.ContainerStateWaiting{
											Reason:  "ErrImagePull",
											Message: "pod pending for some reason",
										},
									},
								},
							},
						},
					},
					&batchv1.Job{
						ObjectMeta: metav1.ObjectMeta{
							Name:      pathHash,
							Namespace: "ns-a",
							Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue, BundleUnpackRefLabel: pathHash},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion:         "v1",
									Kind:               "ConfigMap",
									Name:               pathHash,
									Controller:         &blockOwnerDeletion,
									BlockOwnerDeletion: &blockOwnerDeletion,
								},
							},
						},
						Spec: batchv1.JobSpec{
							ActiveDeadlineSeconds: &defaultUnpackTimeoutSeconds,
							BackoffLimit:          &backoffLimit,
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{
									Name: pathHash,
									Labels: map[string]string{
										install.OLMManagedLabelKey: install.OLMManagedLabelValue,
										BundleUnpackRefLabel:       pathHash,
									},
								},
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									SecurityContext: &corev1.PodSecurityContext{
										RunAsNonRoot: ptr.To(bool(true)),
										RunAsUser:    ptr.To(int64(runAsUser)),
										SeccompProfile: &corev1.SeccompProfile{
											Type: corev1.SeccompProfileTypeRuntimeDefault,
										},
									},
									Containers: []corev1.Container{
										{
											Name:    "extract",
											Image:   opmImage,
											Command: []string{"opm", "alpha", "bundle", "extract", "-m", "/bundle/", "-n", "ns-a", "-c", pathHash, "-z"},
											Env: []corev1.EnvVar{
												{
													Name:  configmap.EnvContainerImage,
													Value: bundlePath,
												},
											},
											VolumeMounts: []corev1.VolumeMount{
												{
													Name:      "bundle",
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
												ReadOnlyRootFilesystem:   ptr.To(true),
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
											Image:   utilImage,
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
												ReadOnlyRootFilesystem:   ptr.To(true),
												Capabilities: &corev1.Capabilities{
													Drop: []corev1.Capability{"ALL"},
												},
											},
											TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
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
											SecurityContext: &corev1.SecurityContext{
												AllowPrivilegeEscalation: ptr.To(bool(false)),
												ReadOnlyRootFilesystem:   ptr.To(true),
												Capabilities: &corev1.Capabilities{
													Drop: []corev1.Capability{"ALL"},
												},
											},
											TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
										},
									},
									Volumes: []corev1.Volume{
										{
											Name: "bundle",
											VolumeSource: corev1.VolumeSource{
												EmptyDir: &corev1.EmptyDirVolumeSource{},
											},
										},
										{
											Name: "util",
											VolumeSource: corev1.VolumeSource{
												EmptyDir: &corev1.EmptyDirVolumeSource{},
											},
										},
									},
									NodeSelector: map[string]string{
										"kubernetes.io/os": "linux",
									},
									Tolerations: podTolerations,
								},
							},
						},
					},
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      pathHash,
							Namespace: "ns-a",
							Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion:         "operators.coreos.com/v1alpha1",
									Kind:               "CatalogSource",
									Name:               "src-a",
									Controller:         &blockOwnerDeletion,
									BlockOwnerDeletion: &blockOwnerDeletion,
								},
							},
						},
					},
				},
				crs: []runtime.Object{
					&operatorsv1alpha1.CatalogSource{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "ns-a",
							Name:      "src-a",
						},
					},
				},
			},

			args: args{
				annotationTimeout: -1 * time.Minute,
				lookup: &operatorsv1alpha1.BundleLookup{
					Path:     bundlePath,
					Replaces: "",
					CatalogSourceRef: &corev1.ObjectReference{
						Namespace: "ns-a",
						Name:      "src-a",
					},
					Conditions: []operatorsv1alpha1.BundleLookupCondition{
						{
							Type:               operatorsv1alpha1.BundleLookupPending,
							Status:             corev1.ConditionTrue,
							Reason:             JobIncompleteReason,
							Message:            JobIncompleteMessage,
							LastTransitionTime: &start,
						},
					},
				},
			},

			expected: expected{
				res: &BundleUnpackResult{
					name: pathHash,
					BundleLookup: &operatorsv1alpha1.BundleLookup{
						Path:     bundlePath,
						Replaces: "",
						CatalogSourceRef: &corev1.ObjectReference{
							Namespace: "ns-a",
							Name:      "src-a",
						},
						Conditions: []operatorsv1alpha1.BundleLookupCondition{
							{
								Type:   operatorsv1alpha1.BundleLookupPending,
								Status: corev1.ConditionTrue,
								Reason: JobIncompleteReason,
								Message: fmt.Sprintf("%s: Unpack pod(ns-a/%s) container(pull) is pending. Reason: ErrImagePull, Message: pod pending for some reason",
									JobIncompleteMessage, pathHash+"-pod"),
								LastTransitionTime: &start,
							},
						},
					},
				},
			},
		},
		{
			description: "CatalogSourcePresent/JobFailed/BundleLookupFailed/WithJobFailReasonNoLabel",
			fields: fields{
				objs: []runtime.Object{
					&batchv1.Job{
						ObjectMeta: metav1.ObjectMeta{
							Name:      pathHash,
							Namespace: "ns-a",
							//omit the "operatorframework.io/bundle-unpack-ref" label
							Labels: map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion:         "v1",
									Kind:               "ConfigMap",
									Name:               pathHash,
									Controller:         &blockOwnerDeletion,
									BlockOwnerDeletion: &blockOwnerDeletion,
								},
							},
						},
						Spec: batchv1.JobSpec{
							ActiveDeadlineSeconds: &defaultUnpackTimeoutSeconds,
							BackoffLimit:          &backoffLimit,
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{
									Name: pathHash,
									Labels: map[string]string{
										install.OLMManagedLabelKey: install.OLMManagedLabelValue,
										BundleUnpackRefLabel:       pathHash,
									},
								},
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									SecurityContext: &corev1.PodSecurityContext{
										RunAsNonRoot: ptr.To(bool(true)),
										RunAsUser:    ptr.To(int64(runAsUser)),
										SeccompProfile: &corev1.SeccompProfile{
											Type: corev1.SeccompProfileTypeRuntimeDefault,
										},
									},
									Containers: []corev1.Container{
										{
											Name:    "extract",
											Image:   opmImage,
											Command: []string{"opm", "alpha", "bundle", "extract", "-m", "/bundle/", "-n", "ns-a", "-c", pathHash, "-z"},
											Env: []corev1.EnvVar{
												{
													Name:  configmap.EnvContainerImage,
													Value: bundlePath,
												},
											},
											VolumeMounts: []corev1.VolumeMount{
												{
													Name:      "bundle",
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
												ReadOnlyRootFilesystem:   ptr.To(true),
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
											Image:   utilImage,
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
												ReadOnlyRootFilesystem:   ptr.To(true),
												Capabilities: &corev1.Capabilities{
													Drop: []corev1.Capability{"ALL"},
												},
											},
											TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
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
											SecurityContext: &corev1.SecurityContext{
												AllowPrivilegeEscalation: ptr.To(bool(false)),
												ReadOnlyRootFilesystem:   ptr.To(true),
												Capabilities: &corev1.Capabilities{
													Drop: []corev1.Capability{"ALL"},
												},
											},
											TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
										},
									},
									Volumes: []corev1.Volume{
										{
											Name: "bundle",
											VolumeSource: corev1.VolumeSource{
												EmptyDir: &corev1.EmptyDirVolumeSource{},
											},
										},
										{
											Name: "util",
											VolumeSource: corev1.VolumeSource{
												EmptyDir: &corev1.EmptyDirVolumeSource{},
											},
										},
									},
									NodeSelector: map[string]string{
										"kubernetes.io/os": "linux",
									},
									Tolerations: podTolerations,
								},
							},
						},
						Status: batchv1.JobStatus{
							Failed: 1,
							Conditions: []batchv1.JobCondition{
								{

									Type:    batchv1.JobFailed,
									Status:  corev1.ConditionTrue,
									Message: "Job was active longer than specified deadline",
									Reason:  "DeadlineExceeded",
								},
							},
						},
					},
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      pathHash,
							Namespace: "ns-a",
							Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion:         "operators.coreos.com/v1alpha1",
									Kind:               "CatalogSource",
									Name:               "src-a",
									Controller:         &blockOwnerDeletion,
									BlockOwnerDeletion: &blockOwnerDeletion,
								},
							},
						},
					},
				},
				crs: []runtime.Object{
					&operatorsv1alpha1.CatalogSource{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "ns-a",
							Name:      "src-a",
						},
					},
				},
			},
			args: args{
				annotationTimeout: -1 * time.Minute,
				lookup: &operatorsv1alpha1.BundleLookup{
					Path:     bundlePath,
					Replaces: "",
					CatalogSourceRef: &corev1.ObjectReference{
						Namespace: "ns-a",
						Name:      "src-a",
					},
					Conditions: []operatorsv1alpha1.BundleLookupCondition{
						{
							Type:               operatorsv1alpha1.BundleLookupPending,
							Status:             corev1.ConditionTrue,
							Reason:             JobIncompleteReason,
							Message:            JobIncompleteMessage,
							LastTransitionTime: &start,
						},
					},
				},
			},
			expected: expected{
				// If job is not found due to missing "operatorframework.io/bundle-unpack-ref" label,
				// we will get an 'AlreadyExists' error in this test when the new job is created
				err: nil,
				res: &BundleUnpackResult{
					name: pathHash,
					BundleLookup: &operatorsv1alpha1.BundleLookup{
						Path:     bundlePath,
						Replaces: "",
						CatalogSourceRef: &corev1.ObjectReference{
							Namespace: "ns-a",
							Name:      "src-a",
						},
						Conditions: []operatorsv1alpha1.BundleLookupCondition{
							{
								Type:               operatorsv1alpha1.BundleLookupPending,
								Status:             corev1.ConditionTrue,
								Reason:             JobIncompleteReason,
								Message:            JobIncompleteMessage,
								LastTransitionTime: &start,
							},
							{
								Type:               operatorsv1alpha1.BundleLookupFailed,
								Status:             corev1.ConditionTrue,
								Reason:             "DeadlineExceeded",
								Message:            "Job was active longer than specified deadline",
								LastTransitionTime: &start,
							},
						},
					},
				},
				jobs: []*batchv1.Job{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      pathHash,
							Namespace: "ns-a",
							Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion:         "v1",
									Kind:               "ConfigMap",
									Name:               pathHash,
									Controller:         &blockOwnerDeletion,
									BlockOwnerDeletion: &blockOwnerDeletion,
								},
							},
						},
						Spec: batchv1.JobSpec{
							ActiveDeadlineSeconds: &defaultUnpackTimeoutSeconds,
							BackoffLimit:          &backoffLimit,
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{
									Name: pathHash,
									Labels: map[string]string{
										install.OLMManagedLabelKey: install.OLMManagedLabelValue,
										BundleUnpackRefLabel:       pathHash,
									},
								},
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									SecurityContext: &corev1.PodSecurityContext{
										RunAsNonRoot: ptr.To(bool(true)),
										RunAsUser:    ptr.To(int64(runAsUser)),
										SeccompProfile: &corev1.SeccompProfile{
											Type: corev1.SeccompProfileTypeRuntimeDefault,
										},
									},
									Containers: []corev1.Container{
										{
											Name:    "extract",
											Image:   opmImage,
											Command: []string{"opm", "alpha", "bundle", "extract", "-m", "/bundle/", "-n", "ns-a", "-c", pathHash, "-z"},
											Env: []corev1.EnvVar{
												{
													Name:  configmap.EnvContainerImage,
													Value: bundlePath,
												},
											},
											VolumeMounts: []corev1.VolumeMount{
												{
													Name:      "bundle",
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
												ReadOnlyRootFilesystem:   ptr.To(true),
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
											Image:   utilImage,
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
												ReadOnlyRootFilesystem:   ptr.To(true),
												Capabilities: &corev1.Capabilities{
													Drop: []corev1.Capability{"ALL"},
												},
											},
											TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
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
											SecurityContext: &corev1.SecurityContext{
												AllowPrivilegeEscalation: ptr.To(bool(false)),
												ReadOnlyRootFilesystem:   ptr.To(true),
												Capabilities: &corev1.Capabilities{
													Drop: []corev1.Capability{"ALL"},
												},
											},
											TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
										},
									},
									Volumes: []corev1.Volume{
										{
											Name: "bundle",
											VolumeSource: corev1.VolumeSource{
												EmptyDir: &corev1.EmptyDirVolumeSource{},
											},
										},
										{
											Name: "util",
											VolumeSource: corev1.VolumeSource{
												EmptyDir: &corev1.EmptyDirVolumeSource{},
											},
										},
									},
									NodeSelector: map[string]string{
										"kubernetes.io/os": "linux",
									},
									Tolerations: podTolerations,
								},
							},
						},
						Status: batchv1.JobStatus{
							Failed: 1,
							Conditions: []batchv1.JobCondition{
								{
									Type:    batchv1.JobFailed,
									Status:  corev1.ConditionTrue,
									Message: "Job was active longer than specified deadline",
									Reason:  "DeadlineExceeded",
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			client := k8sfake.NewSimpleClientset(tt.fields.objs...)

			period := 5 * time.Minute
			factory := informers.NewSharedInformerFactory(client, period)
			configMapInformer := informers.NewSharedInformerFactoryWithOptions(client, period, informers.WithTweakListOptions(func(options *metav1.ListOptions) {
				options.LabelSelector = install.OLMManagedLabelKey
			})).Core().V1().ConfigMaps()
			cmLister := configMapInformer.Lister()
			jobLister := factory.Batch().V1().Jobs().Lister()
			podLister := factory.Core().V1().Pods().Lister()
			roleLister := factory.Rbac().V1().Roles().Lister()
			rbLister := factory.Rbac().V1().RoleBindings().Lister()

			stop := make(chan struct{})
			defer close(stop)

			factory.Start(stop)
			factory.WaitForCacheSync(context.Background().Done())

			crClient := crfake.NewSimpleClientset(tt.fields.crs...)
			crFactory := crinformers.NewSharedInformerFactory(crClient, period)
			csLister := crFactory.Operators().V1alpha1().CatalogSources().Lister()
			crFactory.Start(stop)
			crFactory.WaitForCacheSync(context.Background().Done())

			unpacker, err := NewConfigmapUnpacker(
				WithClient(client),
				WithCatalogSourceLister(csLister),
				WithConfigMapLister(cmLister),
				WithJobLister(jobLister),
				WithPodLister(podLister),
				WithRoleLister(roleLister),
				WithRoleBindingLister(rbLister),
				WithOPMImage(opmImage),
				WithUtilImage(utilImage),
				WithNow(now),
				WithUnpackTimeout(defaultUnpackDuration),
				WithUserID(int64(runAsUser)),
			)
			require.NoError(t, err)

			res, err := unpacker.UnpackBundle(tt.args.lookup, tt.args.annotationTimeout, 0)
			require.Equal(t, tt.expected.err, err)

			if tt.expected.res == nil {
				require.Nil(t, res)
			} else {
				if tt.expected.res.bundle == nil {
					require.Nil(t, res.bundle)
				} else {
					require.NotNil(t, res.bundle)
					require.Equal(t, tt.expected.res.bundle.CsvJson, res.bundle.CsvJson)
					require.Equal(t, tt.expected.res.bundle.CsvName, res.bundle.CsvName)
					require.Equal(t, tt.expected.res.bundle.Version, res.bundle.Version)
					require.Equal(t, tt.expected.res.bundle.SkipRange, res.bundle.SkipRange)
					require.Equal(t, tt.expected.res.bundle.ProvidedApis, res.bundle.ProvidedApis)
					require.Equal(t, tt.expected.res.bundle.RequiredApis, res.bundle.RequiredApis)
					require.Equal(t, tt.expected.res.bundle.PackageName, res.bundle.PackageName)
					require.Equal(t, tt.expected.res.bundle.ChannelName, res.bundle.ChannelName)

					// Object order is not stable, so perform a set based assertion
					require.ElementsMatch(t, tt.expected.res.bundle.Object, res.bundle.Object)
				}
				require.Equal(t, tt.expected.res.Path, res.Path)
				require.Equal(t, tt.expected.res.Replaces, res.Replaces)
				require.Equal(t, tt.expected.res.CatalogSourceRef, res.CatalogSourceRef)
				require.ElementsMatch(t, tt.expected.res.Conditions, res.Conditions)
			}

			opts := metav1.GetOptions{}
			for _, job := range tt.expected.jobs {
				stored, err := client.BatchV1().Jobs(job.GetNamespace()).Get(context.TODO(), job.GetName(), opts)
				require.NoError(t, err)
				require.Equal(t, job, stored)
			}

			for _, cm := range tt.expected.configMaps {
				stored, err := client.CoreV1().ConfigMaps(cm.GetNamespace()).Get(context.TODO(), cm.GetName(), opts)
				require.NoError(t, err)
				require.Equal(t, cm, stored)
			}

			for _, role := range tt.expected.roles {
				stored, err := client.RbacV1().Roles(role.GetNamespace()).Get(context.TODO(), role.GetName(), opts)
				require.NoError(t, err)
				require.Equal(t, role, stored)
			}

			for _, rb := range tt.expected.roleBindings {
				stored, err := client.RbacV1().RoleBindings(rb.GetNamespace()).Get(context.TODO(), rb.GetName(), opts)
				require.NoError(t, err)
				require.Equal(t, rb, stored)
			}
		})
	}
}

func TestOperatorGroupBundleUnpackTimeout(t *testing.T) {
	nsName := "fake-ns"

	for _, tc := range []struct {
		name            string
		operatorGroups  []*operatorsv1.OperatorGroup
		expectedTimeout time.Duration
		expectedError   error
	}{
		{
			name:            "No operator groups exist",
			expectedTimeout: -1 * time.Minute,
			expectedError:   errors.New("found 0 operatorGroups, expected 1"),
		},
		{
			name: "Multiple operator groups exist",
			operatorGroups: []*operatorsv1.OperatorGroup{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       operatorsv1.OperatorGroupKind,
						APIVersion: operatorsv1.GroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "og1",
						Namespace: nsName,
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       operatorsv1.OperatorGroupKind,
						APIVersion: operatorsv1.GroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "og2",
						Namespace: nsName,
					},
				},
			},
			expectedTimeout: -1 * time.Minute,
			expectedError:   errors.New("found 2 operatorGroups, expected 1"),
		},
		{
			name: "One operator group exists with valid timeout annotation",
			operatorGroups: []*operatorsv1.OperatorGroup{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       operatorsv1.OperatorGroupKind,
						APIVersion: operatorsv1.GroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:        "og",
						Namespace:   nsName,
						Annotations: map[string]string{BundleUnpackTimeoutAnnotationKey: "1m"},
					},
				},
			},
			expectedTimeout: 1 * time.Minute,
			expectedError:   nil,
		},
		{
			name: "One operator group exists with no timeout annotation",
			operatorGroups: []*operatorsv1.OperatorGroup{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       operatorsv1.OperatorGroupKind,
						APIVersion: operatorsv1.GroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "og",
						Namespace: nsName,
					},
				},
			},
			expectedTimeout: -1 * time.Minute,
		},
		{
			name: "One operator group exists with invalid timeout annotation",
			operatorGroups: []*operatorsv1.OperatorGroup{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       operatorsv1.OperatorGroupKind,
						APIVersion: operatorsv1.GroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:        "og",
						Namespace:   nsName,
						Annotations: map[string]string{BundleUnpackTimeoutAnnotationKey: "invalid"},
					},
				},
			},
			expectedTimeout: -1 * time.Minute,
			expectedError:   fmt.Errorf("failed to parse unpack timeout annotation(operatorframework.io/bundle-unpack-timeout: invalid): %w", errors.New("time: invalid duration \"invalid\"")),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ogIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			ogLister := v1listers.NewOperatorGroupLister(ogIndexer).OperatorGroups(nsName)

			for _, og := range tc.operatorGroups {
				err := ogIndexer.Add(og)
				assert.NoError(t, err)
			}

			timeout, err := OperatorGroupBundleUnpackTimeout(ogLister)

			assert.Equal(t, tc.expectedTimeout, timeout)
			assert.Equal(t, tc.expectedError, err)
		})
	}
}

func TestOperatorGroupBundleUnpackRetryInterval(t *testing.T) {
	nsName := "fake-ns"

	for _, tc := range []struct {
		name            string
		operatorGroups  []*operatorsv1.OperatorGroup
		expectedTimeout time.Duration
		expectedError   error
	}{
		{
			name:            "No operator groups exist",
			expectedTimeout: 0,
			expectedError:   errors.New("found 0 operatorGroups, expected 1"),
		},
		{
			name: "Multiple operator groups exist",
			operatorGroups: []*operatorsv1.OperatorGroup{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       operatorsv1.OperatorGroupKind,
						APIVersion: operatorsv1.GroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "og1",
						Namespace: nsName,
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       operatorsv1.OperatorGroupKind,
						APIVersion: operatorsv1.GroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "og2",
						Namespace: nsName,
					},
				},
			},
			expectedTimeout: 0,
			expectedError:   errors.New("found 2 operatorGroups, expected 1"),
		},
		{
			name: "One operator group exists with valid unpack retry annotation",
			operatorGroups: []*operatorsv1.OperatorGroup{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       operatorsv1.OperatorGroupKind,
						APIVersion: operatorsv1.GroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:        "og",
						Namespace:   nsName,
						Annotations: map[string]string{BundleUnpackRetryMinimumIntervalAnnotationKey: "1m"},
					},
				},
			},
			expectedTimeout: 1 * time.Minute,
			expectedError:   nil,
		},
		{
			name: "One operator group exists with no unpack retry annotation",
			operatorGroups: []*operatorsv1.OperatorGroup{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       operatorsv1.OperatorGroupKind,
						APIVersion: operatorsv1.GroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "og",
						Namespace: nsName,
					},
				},
			},
			expectedTimeout: 0,
			expectedError:   nil,
		},
		{
			name: "One operator group exists with invalid unpack retry annotation",
			operatorGroups: []*operatorsv1.OperatorGroup{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       operatorsv1.OperatorGroupKind,
						APIVersion: operatorsv1.GroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:        "og",
						Namespace:   nsName,
						Annotations: map[string]string{BundleUnpackRetryMinimumIntervalAnnotationKey: "invalid"},
					},
				},
			},
			expectedTimeout: 0,
			expectedError:   fmt.Errorf("failed to parse unpack retry annotation(operatorframework.io/bundle-unpack-min-retry-interval: invalid): %w", errors.New("time: invalid duration \"invalid\"")),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ogIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			ogLister := v1listers.NewOperatorGroupLister(ogIndexer).OperatorGroups(nsName)

			for _, og := range tc.operatorGroups {
				err := ogIndexer.Add(og)
				assert.NoError(t, err)
			}

			timeout, err := OperatorGroupBundleUnpackRetryInterval(ogLister)

			assert.Equal(t, tc.expectedTimeout, timeout)
			assert.Equal(t, tc.expectedError, err)
		})
	}
}

func TestSortUnpackJobs(t *testing.T) {
	// if there is a non-failed job, it should be first
	// otherwise, the latest job should be first
	//first n-1 jobs and oldest job are preserved
	testJob := func(name string, failed bool, ts int64) *batchv1.Job {
		conditions := []batchv1.JobCondition{}
		if failed {
			conditions = append(conditions, batchv1.JobCondition{
				Type:               batchv1.JobFailed,
				Status:             corev1.ConditionTrue,
				LastTransitionTime: metav1.Time{Time: time.Unix(ts, 0)},
			})
		}
		return &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:   name,
				Labels: map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue, BundleUnpackRefLabel: "test"},
			},
			Status: batchv1.JobStatus{
				Conditions: conditions,
			},
		}
	}
	nilConditionJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "nc",
			Labels: map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue, BundleUnpackRefLabel: "test"},
		},
		Status: batchv1.JobStatus{
			Conditions: nil,
		},
	}
	failedJobs := []*batchv1.Job{
		testJob("f-1", true, 1),
		testJob("f-2", true, 2),
		testJob("f-3", true, 3),
		testJob("f-4", true, 4),
		testJob("f-5", true, 5),
	}
	nonFailedJob := testJob("s-1", false, 1)
	for _, tc := range []struct {
		name             string
		jobs             []*batchv1.Job
		maxRetained      int
		expectedLatest   *batchv1.Job
		expectedToDelete []*batchv1.Job
	}{
		{
			name:        "no job history",
			maxRetained: 0,
			jobs: []*batchv1.Job{
				failedJobs[1],
				failedJobs[2],
				failedJobs[0],
			},
			expectedLatest: failedJobs[2],
			expectedToDelete: []*batchv1.Job{
				failedJobs[1],
				failedJobs[0],
			},
		}, {
			name:        "empty job list",
			maxRetained: 1,
		}, {
			name:        "nil job in list",
			maxRetained: 1,
			jobs: []*batchv1.Job{
				failedJobs[2],
				nil,
				failedJobs[1],
			},
			expectedLatest: failedJobs[2],
		}, {
			name:        "nil condition",
			maxRetained: 3,
			jobs: []*batchv1.Job{
				failedJobs[2],
				nilConditionJob,
				failedJobs[1],
			},
			expectedLatest: nilConditionJob,
		}, {
			name:        "retain oldest",
			maxRetained: 1,
			jobs: []*batchv1.Job{
				failedJobs[2],
				failedJobs[0],
				failedJobs[1],
			},
			expectedToDelete: []*batchv1.Job{
				failedJobs[1],
			},
			expectedLatest: failedJobs[2],
		}, {
			name:        "multiple old jobs",
			maxRetained: 2,
			jobs: []*batchv1.Job{
				failedJobs[1],
				failedJobs[0],
				failedJobs[2],
				failedJobs[3],
				failedJobs[4],
			},
			expectedLatest: failedJobs[4],
			expectedToDelete: []*batchv1.Job{
				failedJobs[1],
				failedJobs[2],
			},
		}, {
			name:        "select non-failed as latest",
			maxRetained: 3,
			jobs: []*batchv1.Job{
				failedJobs[0],
				failedJobs[1],
				nonFailedJob,
			},
			expectedLatest: nonFailedJob,
		},
	} {
		latest, toDelete := sortUnpackJobs(tc.jobs, tc.maxRetained)
		assert.Equal(t, tc.expectedLatest, latest)
		assert.ElementsMatch(t, tc.expectedToDelete, toDelete)
	}
}

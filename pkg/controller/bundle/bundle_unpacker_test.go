package bundle

import (
	"context"
	"testing"
	"time"

	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/configmap"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	crfake "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	crinformers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
)

const (
	csvJson     = "{\"apiVersion\":\"operators.coreos.com/v1alpha1\",\"kind\":\"ClusterServiceVersion\",\"metadata\":{\"annotations\":{\"olm.skipRange\":\"\\u003c 0.6.0\",\"tectonic-visibility\":\"ocs\"},\"name\":\"etcdoperator.v0.9.2\",\"namespace\":\"placeholder\"},\"spec\":{\"customresourcedefinitions\":{\"owned\":[{\"description\":\"Represents a cluster of etcd nodes.\",\"displayName\":\"etcd Cluster\",\"kind\":\"EtcdCluster\",\"name\":\"etcdclusters.etcd.database.coreos.com\",\"resources\":[{\"kind\":\"Service\",\"version\":\"v1\"},{\"kind\":\"Pod\",\"version\":\"v1\"}],\"specDescriptors\":[{\"description\":\"The desired number of member Pods for the etcd cluster.\",\"displayName\":\"Size\",\"path\":\"size\",\"x-descriptors\":[\"urn:alm:descriptor:com.tectonic.ui:podCount\"]},{\"description\":\"Limits describes the minimum/maximum amount of compute resources required/allowed\",\"displayName\":\"Resource Requirements\",\"path\":\"pod.resources\",\"x-descriptors\":[\"urn:alm:descriptor:com.tectonic.ui:resourceRequirements\"]}],\"statusDescriptors\":[{\"description\":\"The status of each of the member Pods for the etcd cluster.\",\"displayName\":\"Member Status\",\"path\":\"members\",\"x-descriptors\":[\"urn:alm:descriptor:com.tectonic.ui:podStatuses\"]},{\"description\":\"The service at which the running etcd cluster can be accessed.\",\"displayName\":\"Service\",\"path\":\"serviceName\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes:Service\"]},{\"description\":\"The current size of the etcd cluster.\",\"displayName\":\"Cluster Size\",\"path\":\"size\"},{\"description\":\"The current version of the etcd cluster.\",\"displayName\":\"Current Version\",\"path\":\"currentVersion\"},{\"description\":\"The target version of the etcd cluster, after upgrading.\",\"displayName\":\"Target Version\",\"path\":\"targetVersion\"},{\"description\":\"The current status of the etcd cluster.\",\"displayName\":\"Status\",\"path\":\"phase\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes.phase\"]},{\"description\":\"Explanation for the current status of the cluster.\",\"displayName\":\"Status Details\",\"path\":\"reason\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes.phase:reason\"]}],\"version\":\"v1beta2\"},{\"description\":\"Represents the intent to backup an etcd cluster.\",\"displayName\":\"etcd Backup\",\"kind\":\"EtcdBackup\",\"name\":\"etcdbackups.etcd.database.coreos.com\",\"specDescriptors\":[{\"description\":\"Specifies the endpoints of an etcd cluster.\",\"displayName\":\"etcd Endpoint(s)\",\"path\":\"etcdEndpoints\",\"x-descriptors\":[\"urn:alm:descriptor:etcd:endpoint\"]},{\"description\":\"The full AWS S3 path where the backup is saved.\",\"displayName\":\"S3 Path\",\"path\":\"s3.path\",\"x-descriptors\":[\"urn:alm:descriptor:aws:s3:path\"]},{\"description\":\"The name of the secret object that stores the AWS credential and config files.\",\"displayName\":\"AWS Secret\",\"path\":\"s3.awsSecret\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes:Secret\"]}],\"statusDescriptors\":[{\"description\":\"Indicates if the backup was successful.\",\"displayName\":\"Succeeded\",\"path\":\"succeeded\",\"x-descriptors\":[\"urn:alm:descriptor:text\"]},{\"description\":\"Indicates the reason for any backup related failures.\",\"displayName\":\"Reason\",\"path\":\"reason\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes.phase:reason\"]}],\"version\":\"v1beta2\"},{\"description\":\"Represents the intent to restore an etcd cluster from a backup.\",\"displayName\":\"etcd Restore\",\"kind\":\"EtcdRestore\",\"name\":\"etcdrestores.etcd.database.coreos.com\",\"specDescriptors\":[{\"description\":\"References the EtcdCluster which should be restored,\",\"displayName\":\"etcd Cluster\",\"path\":\"etcdCluster.name\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes:EtcdCluster\",\"urn:alm:descriptor:text\"]},{\"description\":\"The full AWS S3 path where the backup is saved.\",\"displayName\":\"S3 Path\",\"path\":\"s3.path\",\"x-descriptors\":[\"urn:alm:descriptor:aws:s3:path\"]},{\"description\":\"The name of the secret object that stores the AWS credential and config files.\",\"displayName\":\"AWS Secret\",\"path\":\"s3.awsSecret\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes:Secret\"]}],\"statusDescriptors\":[{\"description\":\"Indicates if the restore was successful.\",\"displayName\":\"Succeeded\",\"path\":\"succeeded\",\"x-descriptors\":[\"urn:alm:descriptor:text\"]},{\"description\":\"Indicates the reason for any restore related failures.\",\"displayName\":\"Reason\",\"path\":\"reason\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes.phase:reason\"]}],\"version\":\"v1beta2\"}],\"required\":[{\"description\":\"Represents a cluster of etcd nodes.\",\"displayName\":\"etcd Cluster\",\"kind\":\"EtcdCluster\",\"name\":\"etcdclusters.etcd.database.coreos.com\",\"resources\":[{\"kind\":\"Service\",\"version\":\"v1\"},{\"kind\":\"Pod\",\"version\":\"v1\"}],\"specDescriptors\":[{\"description\":\"The desired number of member Pods for the etcd cluster.\",\"displayName\":\"Size\",\"path\":\"size\",\"x-descriptors\":[\"urn:alm:descriptor:com.tectonic.ui:podCount\"]}],\"version\":\"v1beta2\"}]},\"description\":\"etcd is a distributed key value store that provides a reliable way to store data across a cluster of machines. It’s open-source and available on GitHub. etcd gracefully handles leader elections during network partitions and will tolerate machine failure, including the leader. Your applications can read and write data into etcd.\\nA simple use-case is to store database connection details or feature flags within etcd as key value pairs. These values can be watched, allowing your app to reconfigure itself when they change. Advanced uses take advantage of the consistency guarantees to implement database leader elections or do distributed locking across a cluster of workers.\\n\\n_The etcd Open Cloud Service is Public Alpha. The goal before Beta is to fully implement backup features._\\n\\n### Reading and writing to etcd\\n\\nCommunicate with etcd though its command line utility `etcdctl` or with the API using the automatically generated Kubernetes Service.\\n\\n[Read the complete guide to using the etcd Open Cloud Service](https://coreos.com/tectonic/docs/latest/alm/etcd-ocs.html)\\n\\n### Supported Features\\n\\n\\n**High availability**\\n\\n\\nMultiple instances of etcd are networked together and secured. Individual failures or networking issues are transparently handled to keep your cluster up and running.\\n\\n\\n**Automated updates**\\n\\n\\nRolling out a new etcd version works like all Kubernetes rolling updates. Simply declare the desired version, and the etcd service starts a safe rolling update to the new version automatically.\\n\\n\\n**Backups included**\\n\\n\\nComing soon, the ability to schedule backups to happen on or off cluster.\\n\",\"displayName\":\"etcd\",\"install\":{\"spec\":{\"deployments\":[{\"name\":\"etcd-operator\",\"spec\":{\"replicas\":1,\"selector\":{\"matchLabels\":{\"name\":\"etcd-operator-alm-owned\"}},\"template\":{\"metadata\":{\"labels\":{\"name\":\"etcd-operator-alm-owned\"},\"name\":\"etcd-operator-alm-owned\"},\"spec\":{\"containers\":[{\"command\":[\"etcd-operator\",\"--create-crd=false\"],\"env\":[{\"name\":\"MY_POD_NAMESPACE\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.namespace\"}}},{\"name\":\"MY_POD_NAME\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.name\"}}}],\"image\":\"quay.io/coreos/etcd-operator@sha256:c0301e4686c3ed4206e370b42de5a3bd2229b9fb4906cf85f3f30650424abec2\",\"name\":\"etcd-operator\"},{\"command\":[\"etcd-backup-operator\",\"--create-crd=false\"],\"env\":[{\"name\":\"MY_POD_NAMESPACE\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.namespace\"}}},{\"name\":\"MY_POD_NAME\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.name\"}}}],\"image\":\"quay.io/coreos/etcd-operator@sha256:c0301e4686c3ed4206e370b42de5a3bd2229b9fb4906cf85f3f30650424abec2\",\"name\":\"etcd-backup-operator\"},{\"command\":[\"etcd-restore-operator\",\"--create-crd=false\"],\"env\":[{\"name\":\"MY_POD_NAMESPACE\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.namespace\"}}},{\"name\":\"MY_POD_NAME\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.name\"}}}],\"image\":\"quay.io/coreos/etcd-operator@sha256:c0301e4686c3ed4206e370b42de5a3bd2229b9fb4906cf85f3f30650424abec2\",\"name\":\"etcd-restore-operator\"}],\"serviceAccountName\":\"etcd-operator\"}}}}],\"permissions\":[{\"rules\":[{\"apiGroups\":[\"etcd.database.coreos.com\"],\"resources\":[\"etcdclusters\",\"etcdbackups\",\"etcdrestores\"],\"verbs\":[\"*\"]},{\"apiGroups\":[\"\"],\"resources\":[\"pods\",\"services\",\"endpoints\",\"persistentvolumeclaims\",\"events\"],\"verbs\":[\"*\"]},{\"apiGroups\":[\"apps\"],\"resources\":[\"deployments\"],\"verbs\":[\"*\"]},{\"apiGroups\":[\"\"],\"resources\":[\"secrets\"],\"verbs\":[\"get\"]}],\"serviceAccountName\":\"etcd-operator\"}]},\"strategy\":\"deployment\"},\"keywords\":[\"etcd\",\"key value\",\"database\",\"coreos\",\"open source\"],\"labels\":{\"alm-owner-etcd\":\"etcdoperator\",\"operated-by\":\"etcdoperator\"},\"links\":[{\"name\":\"Blog\",\"url\":\"https://coreos.com/etcd\"},{\"name\":\"Documentation\",\"url\":\"https://coreos.com/operators/etcd/docs/latest/\"},{\"name\":\"etcd Operator Source Code\",\"url\":\"https://github.com/coreos/etcd-operator\"}],\"maintainers\":[{\"email\":\"support@coreos.com\",\"name\":\"CoreOS, Inc\"}],\"maturity\":\"alpha\",\"provider\":{\"name\":\"CoreOS, Inc\"},\"relatedImages\":[{\"image\":\"quay.io/coreos/etcd@sha256:3816b6daf9b66d6ced6f0f966314e2d4f894982c6b1493061502f8c2bf86ac84\",\"name\":\"etcd-v3.4.0\"},{\"image\":\"quay.io/coreos/etcd@sha256:49d3d4a81e0d030d3f689e7167f23e120abf955f7d08dbedf3ea246485acee9f\",\"name\":\"etcd-3.4.1\"}],\"replaces\":\"etcdoperator.v0.9.0\",\"selector\":{\"matchLabels\":{\"alm-owner-etcd\":\"etcdoperator\",\"operated-by\":\"etcdoperator\"}},\"skips\":[\"etcdoperator.v0.9.1\"],\"version\":\"0.9.2\"}}"
	etcdBackup  = "{\"apiVersion\":\"apiextensions.k8s.io/v1beta1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"name\":\"etcdbackups.etcd.database.coreos.com\"},\"spec\":{\"group\":\"etcd.database.coreos.com\",\"names\":{\"kind\":\"EtcdBackup\",\"listKind\":\"EtcdBackupList\",\"plural\":\"etcdbackups\",\"singular\":\"etcdbackup\"},\"scope\":\"Namespaced\",\"version\":\"v1beta2\"}}"
	etcdCluster = "{\"apiVersion\":\"apiextensions.k8s.io/v1beta1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"name\":\"etcdclusters.etcd.database.coreos.com\"},\"spec\":{\"group\":\"etcd.database.coreos.com\",\"names\":{\"kind\":\"EtcdCluster\",\"listKind\":\"EtcdClusterList\",\"plural\":\"etcdclusters\",\"shortNames\":[\"etcdclus\",\"etcd\"],\"singular\":\"etcdcluster\"},\"scope\":\"Namespaced\",\"version\":\"v1beta2\"}}"
	etcdRestore = "{\"apiVersion\":\"apiextensions.k8s.io/v1beta1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"name\":\"etcdrestores.etcd.database.coreos.com\"},\"spec\":{\"group\":\"etcd.database.coreos.com\",\"names\":{\"kind\":\"EtcdRestore\",\"listKind\":\"EtcdRestoreList\",\"plural\":\"etcdrestores\",\"singular\":\"etcdrestore\"},\"scope\":\"Namespaced\",\"version\":\"v1beta2\"}}"
	opmImage    = "opm-image"
	utilImage   = "util-image"
	bundlePath  = "bundle-path"
)

func TestConfigMapUnpacker(t *testing.T) {
	pathHash := hash(bundlePath)
	start := metav1.Now()
	now := func() metav1.Time {
		return start
	}
	backoffLimit := int32(3)

	type fields struct {
		objs []runtime.Object
		crs  []runtime.Object
	}
	type args struct {
		lookup *operatorsv1alpha1.BundleLookup
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
			description: "CatalogSourcePresent/NoConfigMap/NoJob/Created/Pending",
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
							BackoffLimit: &backoffLimit,
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{
									Name: pathHash,
								},
								Spec: corev1.PodSpec{
									RestartPolicy:    corev1.RestartPolicyNever,
									ImagePullSecrets: []corev1.LocalObjectReference{{Name: "my-secret"}},
									Containers: []corev1.Container{
										{
											Name:    "extract",
											Image:   opmImage,
											Command: []string{"opm", "alpha", "bundle", "extract", "-m", "/bundle/", "-n", "ns-a", "-c", pathHash},
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
			description: "CatalogSourcePresent/ConfigMapPresent/JobPresent/Unpacked",
			fields: fields{
				objs: []runtime.Object{
					&batchv1.Job{
						ObjectMeta: metav1.ObjectMeta{
							Name:      pathHash,
							Namespace: "ns-a",
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
							BackoffLimit: &backoffLimit,
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{
									Name: pathHash,
								},
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:    "extract",
											Image:   opmImage,
											Command: []string{"opm", "alpha", "bundle", "extract", "-m", "/bundle/", "-n", "ns-a", "-c", pathHash},
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
							Name:      pathHash,
							Namespace: "ns-a",
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
							"csv.json":              csvJson,
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
					BundleLookup: &operatorsv1alpha1.BundleLookup{
						Path:     bundlePath,
						Replaces: "",
						CatalogSourceRef: &corev1.ObjectReference{
							Namespace: "ns-a",
							Name:      "src-a",
						},
					},
					name: pathHash,
					bundle: &api.Bundle{
						CsvName: "etcdoperator.v0.9.2",
						CsvJson: csvJson + "\n",
						Object: []string{
							etcdBackup,
							etcdCluster,
							csvJson,
							etcdRestore,
						},
					},
				},
				configMaps: []*corev1.ConfigMap{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      pathHash,
							Namespace: "ns-a",
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
							"csv.json":              csvJson,
							"etcdrestores.crd.json": etcdRestore,
						},
					},
				},
				jobs: []*batchv1.Job{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      pathHash,
							Namespace: "ns-a",
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
							BackoffLimit: &backoffLimit,
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{
									Name: pathHash,
								},
								Spec: corev1.PodSpec{
									RestartPolicy: corev1.RestartPolicyNever,
									Containers: []corev1.Container{
										{
											Name:    "extract",
											Image:   opmImage,
											Command: []string{"opm", "alpha", "bundle", "extract", "-m", "/bundle/", "-n", "ns-a", "-c", pathHash},
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
							Name:      pathHash,
							Namespace: "ns-a",
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
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			client := k8sfake.NewSimpleClientset(tt.fields.objs...)

			period := 5 * time.Minute
			factory := informers.NewSharedInformerFactory(client, period)
			cmLister := factory.Core().V1().ConfigMaps().Lister()
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
			)
			require.NoError(t, err)

			res, err := unpacker.UnpackBundle(tt.args.lookup, map[string]string{})
			require.Equal(t, tt.expected.err, err)

			if tt.expected.res == nil {
				require.Nil(t, res)
			} else {
				if tt.expected.res.bundle == nil {
					require.Nil(t, res.bundle)
				} else {
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

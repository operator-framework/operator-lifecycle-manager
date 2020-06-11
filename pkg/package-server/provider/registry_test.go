//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../client/fakes/fake_registry_client.go github.com/operator-framework/operator-registry/pkg/api.RegistryClient
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../client/fakes/fake_list_packages_client.go github.com/operator-framework/operator-registry/pkg/api.Registry_ListPackagesClient
package provider

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/selection"

	"github.com/operator-framework/operator-registry/pkg/api"
	registryserver "github.com/operator-framework/operator-registry/pkg/server"
	"github.com/operator-framework/operator-registry/pkg/sqlite"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/operators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/client/fakes"
)

const (
	port    = "50054"
	address = "localhost:"
	dbName  = "test.db"
)

func server() {
	_ = os.Remove(dbName)
	lis, err := net.Listen("tcp", "localhost:"+port)
	if err != nil {
		logrus.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer()

	db, err := sql.Open("sqlite3", dbName)
	load, err := sqlite.NewSQLLiteLoader(db)
	if err != nil {
		logrus.Fatal(err)
	}
	if err := load.Migrate(context.TODO()); err != nil {
		logrus.Fatal(err)
	}

	loader := sqlite.NewSQLLoaderForDirectory(load, filepath.Join("testdata", "manifests"))
	if err := loader.Populate(); err != nil {
		logrus.Fatal(err)
	}
	if err := db.Close(); err != nil {
		logrus.Fatal(err)
	}

	store, err := sqlite.NewSQLLiteQuerier(dbName)
	if err != nil {
		logrus.Fatal(err)
	}

	api.RegisterRegistryServer(s, registryserver.NewRegistryServer(store))
	if err := s.Serve(lis); err != nil {
		logrus.Fatalf("failed to serve: %v", err)
	}
}

func NewFakeRegistryProvider(ctx context.Context, clientObjs []runtime.Object, k8sObjs []runtime.Object, globalNamespace string) (*RegistryProvider, error) {
	clientFake := fake.NewSimpleClientset(clientObjs...)
	k8sClientFake := k8sfake.NewSimpleClientset(k8sObjs...)
	opClientFake := operatorclient.NewClient(k8sClientFake, nil, nil)

	op, err := queueinformer.NewOperator(opClientFake.KubernetesInterface().Discovery())
	if err != nil {
		return nil, err
	}

	resyncInterval := 5 * time.Minute

	return NewRegistryProvider(ctx, clientFake, op, resyncInterval, globalNamespace)
}

func catalogSource(name, namespace string) *operatorsv1alpha1.CatalogSource {
	return &operatorsv1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func withRegistryServiceStatus(catalogSource *operatorsv1alpha1.CatalogSource, protocol, serviceName, serviceNamespace, port string, createdAt metav1.Time) *operatorsv1alpha1.CatalogSource {
	out := catalogSource.DeepCopy()
	out.Status.RegistryServiceStatus = &operatorsv1alpha1.RegistryServiceStatus{
		Protocol:         protocol,
		ServiceName:      serviceName,
		ServiceNamespace: serviceNamespace,
		Port:             port,
		CreatedAt:        createdAt,
	}

	return out
}

func TestMain(m *testing.M) {
	go server()
	exit := m.Run()
	if err := os.Remove(dbName); err != nil {
		logrus.Warnf("couldn't remove db")
	}
	os.Exit(exit)
}

var (
	etcdCSVJSON                = "{\"apiVersion\":\"operators.coreos.com/v1alpha1\",\"kind\":\"ClusterServiceVersion\",\"metadata\":{\"annotations\":{\"alm-examples\":\"[{\\\"apiVersion\\\":\\\"etcd.database.coreos.com/v1beta2\\\",\\\"kind\\\":\\\"EtcdCluster\\\",\\\"metadata\\\":{\\\"name\\\":\\\"example\\\",\\\"namespace\\\":\\\"default\\\"},\\\"spec\\\":{\\\"size\\\":3,\\\"version\\\":\\\"3.2.13\\\"}},{\\\"apiVersion\\\":\\\"etcd.database.coreos.com/v1beta2\\\",\\\"kind\\\":\\\"EtcdRestore\\\",\\\"metadata\\\":{\\\"name\\\":\\\"example-etcd-cluster\\\"},\\\"spec\\\":{\\\"etcdCluster\\\":{\\\"name\\\":\\\"example-etcd-cluster\\\"},\\\"backupStorageType\\\":\\\"S3\\\",\\\"s3\\\":{\\\"path\\\":\\\"\\u003cfull-s3-path\\u003e\\\",\\\"awsSecret\\\":\\\"\\u003caws-secret\\u003e\\\"}}},{\\\"apiVersion\\\":\\\"etcd.database.coreos.com/v1beta2\\\",\\\"kind\\\":\\\"EtcdBackup\\\",\\\"metadata\\\":{\\\"name\\\":\\\"example-etcd-cluster-backup\\\"},\\\"spec\\\":{\\\"etcdEndpoints\\\":[\\\"\\u003cetcd-cluster-endpoints\\u003e\\\"],\\\"storageType\\\":\\\"S3\\\",\\\"s3\\\":{\\\"path\\\":\\\"\\u003cfull-s3-path\\u003e\\\",\\\"awsSecret\\\":\\\"\\u003caws-secret\\u003e\\\"}}}]\",\"tectonic-visibility\":\"ocs\"},\"name\":\"etcdoperator.v0.9.2\",\"namespace\":\"placeholder\"},\"spec\":{\"customresourcedefinitions\":{\"owned\":[{\"description\":\"Represents a cluster of etcd nodes.\",\"displayName\":\"etcd Cluster\",\"kind\":\"EtcdCluster\",\"name\":\"etcdclusters.etcd.database.coreos.com\",\"resources\":[{\"kind\":\"Service\",\"version\":\"v1\"},{\"kind\":\"Pod\",\"version\":\"v1\"}],\"specDescriptors\":[{\"description\":\"The desired number of member Pods for the etcd cluster.\",\"displayName\":\"Size\",\"path\":\"size\",\"x-descriptors\":[\"urn:alm:descriptor:com.tectonic.ui:podCount\"]},{\"description\":\"Limits describes the minimum/maximum amount of compute resources required/allowed\",\"displayName\":\"Resource Requirements\",\"path\":\"pod.resources\",\"x-descriptors\":[\"urn:alm:descriptor:com.tectonic.ui:resourceRequirements\"]}],\"statusDescriptors\":[{\"description\":\"The status of each of the member Pods for the etcd cluster.\",\"displayName\":\"Member Status\",\"path\":\"members\",\"x-descriptors\":[\"urn:alm:descriptor:com.tectonic.ui:podStatuses\"]},{\"description\":\"The service at which the running etcd cluster can be accessed.\",\"displayName\":\"Service\",\"path\":\"serviceName\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes:Service\"]},{\"description\":\"The current size of the etcd cluster.\",\"displayName\":\"Cluster Size\",\"path\":\"size\"},{\"description\":\"The current version of the etcd cluster.\",\"displayName\":\"Current Version\",\"path\":\"currentVersion\"},{\"description\":\"The target version of the etcd cluster, after upgrading.\",\"displayName\":\"Target Version\",\"path\":\"targetVersion\"},{\"description\":\"The current status of the etcd cluster.\",\"displayName\":\"Status\",\"path\":\"phase\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes.phase\"]},{\"description\":\"Explanation for the current status of the cluster.\",\"displayName\":\"Status Details\",\"path\":\"reason\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes.phase:reason\"]}],\"version\":\"v1beta2\"},{\"description\":\"Represents the intent to backup an etcd cluster.\",\"displayName\":\"etcd Backup\",\"kind\":\"EtcdBackup\",\"name\":\"etcdbackups.etcd.database.coreos.com\",\"specDescriptors\":[{\"description\":\"Specifies the endpoints of an etcd cluster.\",\"displayName\":\"etcd Endpoint(s)\",\"path\":\"etcdEndpoints\",\"x-descriptors\":[\"urn:alm:descriptor:etcd:endpoint\"]},{\"description\":\"The full AWS S3 path where the backup is saved.\",\"displayName\":\"S3 Path\",\"path\":\"s3.path\",\"x-descriptors\":[\"urn:alm:descriptor:aws:s3:path\"]},{\"description\":\"The name of the secret object that stores the AWS credential and config files.\",\"displayName\":\"AWS Secret\",\"path\":\"s3.awsSecret\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes:Secret\"]}],\"statusDescriptors\":[{\"description\":\"Indicates if the backup was successful.\",\"displayName\":\"Succeeded\",\"path\":\"succeeded\",\"x-descriptors\":[\"urn:alm:descriptor:text\"]},{\"description\":\"Indicates the reason for any backup related failures.\",\"displayName\":\"Reason\",\"path\":\"reason\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes.phase:reason\"]}],\"version\":\"v1beta2\"},{\"description\":\"Represents the intent to restore an etcd cluster from a backup.\",\"displayName\":\"etcd Restore\",\"kind\":\"EtcdRestore\",\"name\":\"etcdrestores.etcd.database.coreos.com\",\"specDescriptors\":[{\"description\":\"References the EtcdCluster which should be restored,\",\"displayName\":\"etcd Cluster\",\"path\":\"etcdCluster.name\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes:EtcdCluster\",\"urn:alm:descriptor:text\"]},{\"description\":\"The full AWS S3 path where the backup is saved.\",\"displayName\":\"S3 Path\",\"path\":\"s3.path\",\"x-descriptors\":[\"urn:alm:descriptor:aws:s3:path\"]},{\"description\":\"The name of the secret object that stores the AWS credential and config files.\",\"displayName\":\"AWS Secret\",\"path\":\"s3.awsSecret\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes:Secret\"]}],\"statusDescriptors\":[{\"description\":\"Indicates if the restore was successful.\",\"displayName\":\"Succeeded\",\"path\":\"succeeded\",\"x-descriptors\":[\"urn:alm:descriptor:text\"]},{\"description\":\"Indicates the reason for any restore related failures.\",\"displayName\":\"Reason\",\"path\":\"reason\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes.phase:reason\"]}],\"version\":\"v1beta2\"}]},\"description\":\"etcd is a distributed key value store that provides a reliable way to store data across a cluster of machines. It’s open-source and available on GitHub. etcd gracefully handles leader elections during network partitions and will tolerate machine failure, including the leader. Your applications can read and write data into etcd.\\nA simple use-case is to store database connection details or feature flags within etcd as key value pairs. These values can be watched, allowing your app to reconfigure itself when they change. Advanced uses take advantage of the consistency guarantees to implement database leader elections or do distributed locking across a cluster of workers.\\n\\n_The etcd Open Cloud Service is Public Alpha. The goal before Beta is to fully implement backup features._\\n\\n### Reading and writing to etcd\\n\\nCommunicate with etcd though its command line utility `etcdctl` or with the API using the automatically generated Kubernetes Service.\\n\\n[Read the complete guide to using the etcd Open Cloud Service](https://coreos.com/tectonic/docs/latest/alm/etcd-ocs.html)\\n\\n### Supported Features\\n\\n\\n**High availability**\\n\\n\\nMultiple instances of etcd are networked together and secured. Individual failures or networking issues are transparently handled to keep your cluster up and running.\\n\\n\\n**Automated updates**\\n\\n\\nRolling out a new etcd version works like all Kubernetes rolling updates. Simply declare the desired version, and the etcd service starts a safe rolling update to the new version automatically.\\n\\n\\n**Backups included**\\n\\n\\nComing soon, the ability to schedule backups to happen on or off cluster.\\n\",\"displayName\":\"etcd\",\"icon\":[{\"base64data\":\"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\",\"mediatype\":\"image/png\"}],\"install\":{\"spec\":{\"deployments\":[{\"name\":\"etcd-operator\",\"spec\":{\"replicas\":1,\"selector\":{\"matchLabels\":{\"name\":\"etcd-operator-alm-owned\"}},\"template\":{\"metadata\":{\"labels\":{\"name\":\"etcd-operator-alm-owned\"},\"name\":\"etcd-operator-alm-owned\"},\"spec\":{\"containers\":[{\"command\":[\"etcd-operator\",\"--create-crd=false\"],\"env\":[{\"name\":\"MY_POD_NAMESPACE\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.namespace\"}}},{\"name\":\"MY_POD_NAME\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.name\"}}}],\"image\":\"quay.io/coreos/etcd-operator@sha256:c0301e4686c3ed4206e370b42de5a3bd2229b9fb4906cf85f3f30650424abec2\",\"name\":\"etcd-operator\"},{\"command\":[\"etcd-backup-operator\",\"--create-crd=false\"],\"env\":[{\"name\":\"MY_POD_NAMESPACE\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.namespace\"}}},{\"name\":\"MY_POD_NAME\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.name\"}}}],\"image\":\"quay.io/coreos/etcd-operator@sha256:c0301e4686c3ed4206e370b42de5a3bd2229b9fb4906cf85f3f30650424abec2\",\"name\":\"etcd-backup-operator\"},{\"command\":[\"etcd-restore-operator\",\"--create-crd=false\"],\"env\":[{\"name\":\"MY_POD_NAMESPACE\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.namespace\"}}},{\"name\":\"MY_POD_NAME\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.name\"}}}],\"image\":\"quay.io/coreos/etcd-operator@sha256:c0301e4686c3ed4206e370b42de5a3bd2229b9fb4906cf85f3f30650424abec2\",\"name\":\"etcd-restore-operator\"}],\"serviceAccountName\":\"etcd-operator\"}}}}],\"permissions\":[{\"rules\":[{\"apiGroups\":[\"etcd.database.coreos.com\"],\"resources\":[\"etcdclusters\",\"etcdbackups\",\"etcdrestores\"],\"verbs\":[\"*\"]},{\"apiGroups\":[\"\"],\"resources\":[\"pods\",\"services\",\"endpoints\",\"persistentvolumeclaims\",\"events\"],\"verbs\":[\"*\"]},{\"apiGroups\":[\"apps\"],\"resources\":[\"deployments\"],\"verbs\":[\"*\"]},{\"apiGroups\":[\"\"],\"resources\":[\"secrets\"],\"verbs\":[\"get\"]}],\"serviceAccountName\":\"etcd-operator\"}]},\"strategy\":\"deployment\"},\"keywords\":[\"etcd\",\"key value\",\"database\",\"coreos\",\"open source\"],\"labels\":{\"alm-owner-etcd\":\"etcdoperator\",\"operated-by\":\"etcdoperator\"},\"links\":[{\"name\":\"Blog\",\"url\":\"https://coreos.com/etcd\"},{\"name\":\"Documentation\",\"url\":\"https://coreos.com/operators/etcd/docs/latest/\"},{\"name\":\"etcd Operator Source Code\",\"url\":\"https://github.com/coreos/etcd-operator\"}],\"maintainers\":[{\"email\":\"support@coreos.com\",\"name\":\"CoreOS, Inc\"}],\"maturity\":\"alpha\",\"provider\":{\"name\":\"CoreOS, Inc\"},\"replaces\":\"etcdoperator.v0.9.0\",\"selector\":{\"matchLabels\":{\"alm-owner-etcd\":\"etcdoperator\",\"operated-by\":\"etcdoperator\"}},\"version\":\"0.9.2\"}}"
	etcdWithLabelsCSVJSON      = "{\"apiVersion\":\"operators.coreos.com/v1alpha1\",\"kind\":\"ClusterServiceVersion\",\"metadata\":{\"labels\": {\"test\": \"label\"},\"annotations\":{\"alm-examples\":\"[{\\\"apiVersion\\\":\\\"etcd.database.coreos.com/v1beta2\\\",\\\"kind\\\":\\\"EtcdCluster\\\",\\\"metadata\\\":{\\\"name\\\":\\\"example\\\",\\\"namespace\\\":\\\"default\\\"},\\\"spec\\\":{\\\"size\\\":3,\\\"version\\\":\\\"3.2.13\\\"}},{\\\"apiVersion\\\":\\\"etcd.database.coreos.com/v1beta2\\\",\\\"kind\\\":\\\"EtcdRestore\\\",\\\"metadata\\\":{\\\"name\\\":\\\"example-etcd-cluster\\\"},\\\"spec\\\":{\\\"etcdCluster\\\":{\\\"name\\\":\\\"example-etcd-cluster\\\"},\\\"backupStorageType\\\":\\\"S3\\\",\\\"s3\\\":{\\\"path\\\":\\\"\\u003cfull-s3-path\\u003e\\\",\\\"awsSecret\\\":\\\"\\u003caws-secret\\u003e\\\"}}},{\\\"apiVersion\\\":\\\"etcd.database.coreos.com/v1beta2\\\",\\\"kind\\\":\\\"EtcdBackup\\\",\\\"metadata\\\":{\\\"name\\\":\\\"example-etcd-cluster-backup\\\"},\\\"spec\\\":{\\\"etcdEndpoints\\\":[\\\"\\u003cetcd-cluster-endpoints\\u003e\\\"],\\\"storageType\\\":\\\"S3\\\",\\\"s3\\\":{\\\"path\\\":\\\"\\u003cfull-s3-path\\u003e\\\",\\\"awsSecret\\\":\\\"\\u003caws-secret\\u003e\\\"}}}]\",\"tectonic-visibility\":\"ocs\"},\"name\":\"etcdoperator.v0.9.2\",\"namespace\":\"placeholder\"},\"spec\":{\"customresourcedefinitions\":{\"owned\":[{\"description\":\"Represents a cluster of etcd nodes.\",\"displayName\":\"etcd Cluster\",\"kind\":\"EtcdCluster\",\"name\":\"etcdclusters.etcd.database.coreos.com\",\"resources\":[{\"kind\":\"Service\",\"version\":\"v1\"},{\"kind\":\"Pod\",\"version\":\"v1\"}],\"specDescriptors\":[{\"description\":\"The desired number of member Pods for the etcd cluster.\",\"displayName\":\"Size\",\"path\":\"size\",\"x-descriptors\":[\"urn:alm:descriptor:com.tectonic.ui:podCount\"]},{\"description\":\"Limits describes the minimum/maximum amount of compute resources required/allowed\",\"displayName\":\"Resource Requirements\",\"path\":\"pod.resources\",\"x-descriptors\":[\"urn:alm:descriptor:com.tectonic.ui:resourceRequirements\"]}],\"statusDescriptors\":[{\"description\":\"The status of each of the member Pods for the etcd cluster.\",\"displayName\":\"Member Status\",\"path\":\"members\",\"x-descriptors\":[\"urn:alm:descriptor:com.tectonic.ui:podStatuses\"]},{\"description\":\"The service at which the running etcd cluster can be accessed.\",\"displayName\":\"Service\",\"path\":\"serviceName\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes:Service\"]},{\"description\":\"The current size of the etcd cluster.\",\"displayName\":\"Cluster Size\",\"path\":\"size\"},{\"description\":\"The current version of the etcd cluster.\",\"displayName\":\"Current Version\",\"path\":\"currentVersion\"},{\"description\":\"The target version of the etcd cluster, after upgrading.\",\"displayName\":\"Target Version\",\"path\":\"targetVersion\"},{\"description\":\"The current status of the etcd cluster.\",\"displayName\":\"Status\",\"path\":\"phase\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes.phase\"]},{\"description\":\"Explanation for the current status of the cluster.\",\"displayName\":\"Status Details\",\"path\":\"reason\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes.phase:reason\"]}],\"version\":\"v1beta2\"},{\"description\":\"Represents the intent to backup an etcd cluster.\",\"displayName\":\"etcd Backup\",\"kind\":\"EtcdBackup\",\"name\":\"etcdbackups.etcd.database.coreos.com\",\"specDescriptors\":[{\"description\":\"Specifies the endpoints of an etcd cluster.\",\"displayName\":\"etcd Endpoint(s)\",\"path\":\"etcdEndpoints\",\"x-descriptors\":[\"urn:alm:descriptor:etcd:endpoint\"]},{\"description\":\"The full AWS S3 path where the backup is saved.\",\"displayName\":\"S3 Path\",\"path\":\"s3.path\",\"x-descriptors\":[\"urn:alm:descriptor:aws:s3:path\"]},{\"description\":\"The name of the secret object that stores the AWS credential and config files.\",\"displayName\":\"AWS Secret\",\"path\":\"s3.awsSecret\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes:Secret\"]}],\"statusDescriptors\":[{\"description\":\"Indicates if the backup was successful.\",\"displayName\":\"Succeeded\",\"path\":\"succeeded\",\"x-descriptors\":[\"urn:alm:descriptor:text\"]},{\"description\":\"Indicates the reason for any backup related failures.\",\"displayName\":\"Reason\",\"path\":\"reason\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes.phase:reason\"]}],\"version\":\"v1beta2\"},{\"description\":\"Represents the intent to restore an etcd cluster from a backup.\",\"displayName\":\"etcd Restore\",\"kind\":\"EtcdRestore\",\"name\":\"etcdrestores.etcd.database.coreos.com\",\"specDescriptors\":[{\"description\":\"References the EtcdCluster which should be restored,\",\"displayName\":\"etcd Cluster\",\"path\":\"etcdCluster.name\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes:EtcdCluster\",\"urn:alm:descriptor:text\"]},{\"description\":\"The full AWS S3 path where the backup is saved.\",\"displayName\":\"S3 Path\",\"path\":\"s3.path\",\"x-descriptors\":[\"urn:alm:descriptor:aws:s3:path\"]},{\"description\":\"The name of the secret object that stores the AWS credential and config files.\",\"displayName\":\"AWS Secret\",\"path\":\"s3.awsSecret\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes:Secret\"]}],\"statusDescriptors\":[{\"description\":\"Indicates if the restore was successful.\",\"displayName\":\"Succeeded\",\"path\":\"succeeded\",\"x-descriptors\":[\"urn:alm:descriptor:text\"]},{\"description\":\"Indicates the reason for any restore related failures.\",\"displayName\":\"Reason\",\"path\":\"reason\",\"x-descriptors\":[\"urn:alm:descriptor:io.kubernetes.phase:reason\"]}],\"version\":\"v1beta2\"}]},\"description\":\"etcd is a distributed key value store that provides a reliable way to store data across a cluster of machines. It’s open-source and available on GitHub. etcd gracefully handles leader elections during network partitions and will tolerate machine failure, including the leader. Your applications can read and write data into etcd.\\nA simple use-case is to store database connection details or feature flags within etcd as key value pairs. These values can be watched, allowing your app to reconfigure itself when they change. Advanced uses take advantage of the consistency guarantees to implement database leader elections or do distributed locking across a cluster of workers.\\n\\n_The etcd Open Cloud Service is Public Alpha. The goal before Beta is to fully implement backup features._\\n\\n### Reading and writing to etcd\\n\\nCommunicate with etcd though its command line utility `etcdctl` or with the API using the automatically generated Kubernetes Service.\\n\\n[Read the complete guide to using the etcd Open Cloud Service](https://coreos.com/tectonic/docs/latest/alm/etcd-ocs.html)\\n\\n### Supported Features\\n\\n\\n**High availability**\\n\\n\\nMultiple instances of etcd are networked together and secured. Individual failures or networking issues are transparently handled to keep your cluster up and running.\\n\\n\\n**Automated updates**\\n\\n\\nRolling out a new etcd version works like all Kubernetes rolling updates. Simply declare the desired version, and the etcd service starts a safe rolling update to the new version automatically.\\n\\n\\n**Backups included**\\n\\n\\nComing soon, the ability to schedule backups to happen on or off cluster.\\n\",\"displayName\":\"etcd\",\"icon\":[{\"base64data\":\"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\",\"mediatype\":\"image/png\"}],\"install\":{\"spec\":{\"deployments\":[{\"name\":\"etcd-operator\",\"spec\":{\"replicas\":1,\"selector\":{\"matchLabels\":{\"name\":\"etcd-operator-alm-owned\"}},\"template\":{\"metadata\":{\"labels\":{\"name\":\"etcd-operator-alm-owned\"},\"name\":\"etcd-operator-alm-owned\"},\"spec\":{\"containers\":[{\"command\":[\"etcd-operator\",\"--create-crd=false\"],\"env\":[{\"name\":\"MY_POD_NAMESPACE\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.namespace\"}}},{\"name\":\"MY_POD_NAME\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.name\"}}}],\"image\":\"quay.io/coreos/etcd-operator@sha256:c0301e4686c3ed4206e370b42de5a3bd2229b9fb4906cf85f3f30650424abec2\",\"name\":\"etcd-operator\"},{\"command\":[\"etcd-backup-operator\",\"--create-crd=false\"],\"env\":[{\"name\":\"MY_POD_NAMESPACE\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.namespace\"}}},{\"name\":\"MY_POD_NAME\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.name\"}}}],\"image\":\"quay.io/coreos/etcd-operator@sha256:c0301e4686c3ed4206e370b42de5a3bd2229b9fb4906cf85f3f30650424abec2\",\"name\":\"etcd-backup-operator\"},{\"command\":[\"etcd-restore-operator\",\"--create-crd=false\"],\"env\":[{\"name\":\"MY_POD_NAMESPACE\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.namespace\"}}},{\"name\":\"MY_POD_NAME\",\"valueFrom\":{\"fieldRef\":{\"fieldPath\":\"metadata.name\"}}}],\"image\":\"quay.io/coreos/etcd-operator@sha256:c0301e4686c3ed4206e370b42de5a3bd2229b9fb4906cf85f3f30650424abec2\",\"name\":\"etcd-restore-operator\"}],\"serviceAccountName\":\"etcd-operator\"}}}}],\"permissions\":[{\"rules\":[{\"apiGroups\":[\"etcd.database.coreos.com\"],\"resources\":[\"etcdclusters\",\"etcdbackups\",\"etcdrestores\"],\"verbs\":[\"*\"]},{\"apiGroups\":[\"\"],\"resources\":[\"pods\",\"services\",\"endpoints\",\"persistentvolumeclaims\",\"events\"],\"verbs\":[\"*\"]},{\"apiGroups\":[\"apps\"],\"resources\":[\"deployments\"],\"verbs\":[\"*\"]},{\"apiGroups\":[\"\"],\"resources\":[\"secrets\"],\"verbs\":[\"get\"]}],\"serviceAccountName\":\"etcd-operator\"}]},\"strategy\":\"deployment\"},\"keywords\":[\"etcd\",\"key value\",\"database\",\"coreos\",\"open source\"],\"labels\":{\"alm-owner-etcd\":\"etcdoperator\",\"operated-by\":\"etcdoperator\"},\"links\":[{\"name\":\"Blog\",\"url\":\"https://coreos.com/etcd\"},{\"name\":\"Documentation\",\"url\":\"https://coreos.com/operators/etcd/docs/latest/\"},{\"name\":\"etcd Operator Source Code\",\"url\":\"https://github.com/coreos/etcd-operator\"}],\"maintainers\":[{\"email\":\"support@coreos.com\",\"name\":\"CoreOS, Inc\"}],\"maturity\":\"alpha\",\"provider\":{\"name\":\"CoreOS, Inc\"},\"replaces\":\"etcdoperator.v0.9.0\",\"selector\":{\"matchLabels\":{\"alm-owner-etcd\":\"etcdoperator\",\"operated-by\":\"etcdoperator\"}},\"version\":\"0.9.2\"}}"
	etcdBackupsCRDJSON         = "{\"apiVersion\":\"apiextensions.k8s.io/v1beta1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"name\":\"etcdbackups.etcd.database.coreos.com\"},\"spec\":{\"group\":\"etcd.database.coreos.com\",\"names\":{\"kind\":\"EtcdBackup\",\"listKind\":\"EtcdBackupList\",\"plural\":\"etcdbackups\",\"singular\":\"etcdbackup\"},\"scope\":\"Namespaced\",\"version\":\"v1beta2\"}}"
	etcdUpgradesCRDJSON        = "{\"apiVersion\":\"apiextensions.k8s.io/v1beta1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"name\":\"etcdclusters.etcd.database.coreos.com\"},\"spec\":{\"group\":\"etcd.database.coreos.com\",\"names\":{\"kind\":\"EtcdCluster\",\"listKind\":\"EtcdClusterList\",\"plural\":\"etcdclusters\",\"shortNames\":[\"etcdclus\",\"etcd\"],\"singular\":\"etcdcluster\"},\"scope\":\"Namespaced\",\"version\":\"v1beta2\"}}"
	etcdRestoresCRDJSON        = "{\"apiVersion\":\"apiextensions.k8s.io/v1beta1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"name\":\"etcdrestores.etcd.database.coreos.com\"},\"spec\":{\"group\":\"etcd.database.coreos.com\",\"names\":{\"kind\":\"EtcdRestore\",\"listKind\":\"EtcdRestoreList\",\"plural\":\"etcdrestores\",\"singular\":\"etcdrestore\"},\"scope\":\"Namespaced\",\"version\":\"v1beta2\"}}"
	prometheusCSVJSON          = `{"apiVersion":"operators.coreos.com/v1alpha1","kind":"ClusterServiceVersion","metadata":{"annotations":{"alm-examples":"[{\"apiVersion\":\"monitoring.coreos.com/v1\",\"kind\":\"Prometheus\",\"metadata\":{\"name\":\"example\",\"labels\":{\"prometheus\":\"k8s\"}},\"spec\":{\"replicas\":2,\"version\":\"v2.3.2\",\"serviceAccountName\":\"prometheus-k8s\",\"securityContext\": {}, \"serviceMonitorSelector\":{\"matchExpressions\":[{\"key\":\"k8s-app\",\"operator\":\"Exists\"}]},\"ruleSelector\":{\"matchLabels\":{\"role\":\"prometheus-rulefiles\",\"prometheus\":\"k8s\"}},\"alerting\":{\"alertmanagers\":[{\"namespace\":\"monitoring\",\"name\":\"alertmanager-main\",\"port\":\"web\"}]}}},{\"apiVersion\":\"monitoring.coreos.com/v1\",\"kind\":\"ServiceMonitor\",\"metadata\":{\"name\":\"example\",\"labels\":{\"k8s-app\":\"prometheus\"}},\"spec\":{\"selector\":{\"matchLabels\":{\"k8s-app\":\"prometheus\"}},\"endpoints\":[{\"port\":\"web\",\"interval\":\"30s\"}]}},{\"apiVersion\":\"monitoring.coreos.com/v1\",\"kind\":\"Alertmanager\",\"metadata\":{\"name\":\"alertmanager-main\"},\"spec\":{\"replicas\":3, \"securityContext\": {}}}]"},"name":"prometheusoperator.0.22.2","namespace":"placeholder"},"spec":{"customresourcedefinitions":{"owned":[{"description":"A running Prometheus instance","displayName":"Prometheus","kind":"Prometheus","name":"prometheuses.monitoring.coreos.com","resources":[{"kind":"StatefulSet","version":"v1beta2"},{"kind":"Pod","version":"v1"}],"specDescriptors":[{"description":"Desired number of Pods for the cluster","displayName":"Size","path":"replicas","x-descriptors":["urn:alm:descriptor:com.tectonic.ui:podCount"]},{"description":"A selector for the ConfigMaps from which to load rule files","displayName":"Rule Config Map Selector","path":"ruleSelector","x-descriptors":["urn:alm:descriptor:com.tectonic.ui:selector:core:v1:ConfigMap"]},{"description":"ServiceMonitors to be selected for target discovery","displayName":"Service Monitor Selector","path":"serviceMonitorSelector","x-descriptors":["urn:alm:descriptor:com.tectonic.ui:selector:monitoring.coreos.com:v1:ServiceMonitor"]},{"description":"The ServiceAccount to use to run the Prometheus pods","displayName":"Service Account","path":"serviceAccountName","x-descriptors":["urn:alm:descriptor:io.kubernetes:ServiceAccount"]},{"description":"Limits describes the minimum/maximum amount of compute resources required/allowed","displayName":"Resource Requirements","path":"resources","x-descriptors":["urn:alm:descriptor:com.tectonic.ui:resourceRequirements"]}],"version":"v1"},{"description":"A Prometheus Rule configures groups of sequentially evaluated recording and alerting rules.","displayName":"Prometheus Rule","kind":"PrometheusRule","name":"prometheusrules.monitoring.coreos.com","version":"v1"},{"description":"Configures prometheus to monitor a particular k8s service","displayName":"Service Monitor","kind":"ServiceMonitor","name":"servicemonitors.monitoring.coreos.com","resources":[{"kind":"Pod","version":"v1"}],"specDescriptors":[{"description":"The label to use to retrieve the job name from","displayName":"Job Label","path":"jobLabel","x-descriptors":["urn:alm:descriptor:com.tectonic.ui:label"]},{"description":"A list of endpoints allowed as part of this ServiceMonitor","displayName":"Endpoints","path":"endpoints","x-descriptors":["urn:alm:descriptor:com.tectonic.ui:endpointList"]}],"version":"v1"},{"description":"Configures an Alertmanager for the namespace","displayName":"Alertmanager","kind":"Alertmanager","name":"alertmanagers.monitoring.coreos.com","resources":[{"kind":"StatefulSet","version":"v1beta2"},{"kind":"Pod","version":"v1"}],"specDescriptors":[{"description":"Desired number of Pods for the cluster","displayName":"Size","path":"replicas","x-descriptors":["urn:alm:descriptor:com.tectonic.ui:podCount"]},{"description":"Limits describes the minimum/maximum amount of compute resources required/allowed","displayName":"Resource Requirements","path":"resources","x-descriptors":["urn:alm:descriptor:com.tectonic.ui:resourceRequirements"]}],"version":"v1"}]},"description":"The Prometheus Operator for Kubernetes provides easy monitoring definitions for Kubernetes services and deployment and management of Prometheus instances.\n\nOnce installed, the Prometheus Operator provides the following features:\n\n* **Create/Destroy**: Easily launch a Prometheus instance for your Kubernetes namespace, a specific application or team easily using the Operator.\n\n* **Simple Configuration**: Configure the fundamentals of Prometheus like versions, persistence, retention policies, and replicas from a native Kubernetes resource.\n\n* **Target Services via Labels**: Automatically generate monitoring target configurations based on familiar Kubernetes label queries; no need to learn a Prometheus specific configuration language.\n\n### Other Supported Features\n\n**High availability**\n\nMultiple instances are run across failure zones and data is replicated. This keeps your monitoring available during an outage, when you need it most.\n\n**Updates via automated operations**\n\nNew Prometheus versions are deployed using a rolling update with no downtime, making it easy to stay up to date.\n\n**Handles the dynamic nature of containers**\n\nAlerting rules are attached to groups of containers instead of individual instances, which is ideal for the highly dynamic nature of container deployment.\n","displayName":"Prometheus Operator","icon":[{"base64data":"PHN2ZyB3aWR0aD0iMjQ5MCIgaGVpZ2h0PSIyNTAwIiB2aWV3Qm94PSIwIDAgMjU2IDI1NyIgeG1sbnM9Imh0dHA6Ly93d3cudzMub3JnLzIwMDAvc3ZnIiBwcmVzZXJ2ZUFzcGVjdFJhdGlvPSJ4TWlkWU1pZCI+PHBhdGggZD0iTTEyOC4wMDEuNjY3QzU3LjMxMS42NjcgMCA1Ny45NzEgMCAxMjguNjY0YzAgNzAuNjkgNTcuMzExIDEyNy45OTggMTI4LjAwMSAxMjcuOTk4UzI1NiAxOTkuMzU0IDI1NiAxMjguNjY0QzI1NiA1Ny45NyAxOTguNjg5LjY2NyAxMjguMDAxLjY2N3ptMCAyMzkuNTZjLTIwLjExMiAwLTM2LjQxOS0xMy40MzUtMzYuNDE5LTMwLjAwNGg3Mi44MzhjMCAxNi41NjYtMTYuMzA2IDMwLjAwNC0zNi40MTkgMzAuMDA0em02MC4xNTMtMzkuOTRINjcuODQyVjE3OC40N2gxMjAuMzE0djIxLjgxNmgtLjAwMnptLS40MzItMzMuMDQ1SDY4LjE4NWMtLjM5OC0uNDU4LS44MDQtLjkxLTEuMTg4LTEuMzc1LTEyLjMxNS0xNC45NTQtMTUuMjE2LTIyLjc2LTE4LjAzMi0zMC43MTYtLjA0OC0uMjYyIDE0LjkzMyAzLjA2IDI1LjU1NiA1LjQ1IDAgMCA1LjQ2NiAxLjI2NSAxMy40NTggMi43MjItNy42NzMtOC45OTQtMTIuMjMtMjAuNDI4LTEyLjIzLTMyLjExNiAwLTI1LjY1OCAxOS42OC00OC4wNzkgMTIuNTgtNjYuMjAxIDYuOTEuNTYyIDE0LjMgMTQuNTgzIDE0LjggMzYuNTA1IDcuMzQ2LTEwLjE1MiAxMC40Mi0yOC42OSAxMC40Mi00MC4wNTYgMC0xMS43NjkgNy43NTUtMjUuNDQgMTUuNTEyLTI1LjkwNy02LjkxNSAxMS4zOTYgMS43OSAyMS4xNjUgOS41MyA0NS40IDIuOTAyIDkuMTAzIDIuNTMyIDI0LjQyMyA0Ljc3MiAzNC4xMzguNzQ0LTIwLjE3OCA0LjIxMy00OS42MiAxNy4wMTQtNTkuNzg0LTUuNjQ3IDEyLjguODM2IDI4LjgxOCA1LjI3IDM2LjUxOCA3LjE1NCAxMi40MjQgMTEuNDkgMjEuODM2IDExLjQ5IDM5LjYzOCAwIDExLjkzNi00LjQwNyAyMy4xNzMtMTEuODQgMzEuOTU4IDguNDUyLTEuNTg2IDE0LjI4OS0zLjAxNiAxNC4yODktMy4wMTZsMjcuNDUtNS4zNTVjLjAwMi0uMDAyLTMuOTg3IDE2LjQwMS0xOS4zMTQgMzIuMTk3eiIgZmlsbD0iI0RBNEUzMSIvPjwvc3ZnPg==","mediatype":"image/svg+xml"}],"install":{"spec":{"deployments":[{"name":"prometheus-operator","spec":{"replicas":1,"selector":{"matchLabels":{"k8s-app":"prometheus-operator"}},"template":{"metadata":{"labels":{"k8s-app":"prometheus-operator"}},"spec":{"containers":[{"args":["-namespace=$(K8S_NAMESPACE)","-manage-crds=false","-logtostderr=true","--config-reloader-image=quay.io/coreos/configmap-reload:v0.0.1","--prometheus-config-reloader=quay.io/coreos/prometheus-config-reloader:v0.22.2"],"env":[{"name":"K8S_NAMESPACE","valueFrom":{"fieldRef":{"fieldPath":"metadata.namespace"}}}],"image":"quay.io/coreos/prometheus-operator@sha256:3daa69a8c6c2f1d35dcf1fe48a7cd8b230e55f5229a1ded438f687debade5bcf","name":"prometheus-operator","ports":[{"containerPort":8080,"name":"http"}],"resources":{"limits":{"cpu":"200m","memory":"100Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"securityContext":{"allowPrivilegeEscalation":false,"readOnlyRootFilesystem":true}}],"nodeSelector":{"kubernetes.io/os":"linux"},"serviceAccount":"prometheus-operator-0-22-2"}}}}],"permissions":[{"rules":[{"apiGroups":[""],"resources":["nodes","services","endpoints","pods"],"verbs":["get","list","watch"]},{"apiGroups":[""],"resources":["configmaps"],"verbs":["get"]}],"serviceAccountName":"prometheus-k8s"},{"rules":[{"apiGroups":["apiextensions.k8s.io"],"resources":["customresourcedefinitions"],"verbs":["*"]},{"apiGroups":["monitoring.coreos.com"],"resources":["alertmanagers","prometheuses","prometheuses/finalizers","alertmanagers/finalizers","servicemonitors","prometheusrules"],"verbs":["*"]},{"apiGroups":["apps"],"resources":["statefulsets"],"verbs":["*"]},{"apiGroups":[""],"resources":["configmaps","secrets"],"verbs":["*"]},{"apiGroups":[""],"resources":["pods"],"verbs":["list","delete"]},{"apiGroups":[""],"resources":["services","endpoints"],"verbs":["get","create","update"]},{"apiGroups":[""],"resources":["nodes"],"verbs":["list","watch"]},{"apiGroups":[""],"resources":["namespaces"],"verbs":["list","watch"]}],"serviceAccountName":"prometheus-operator-0-22-2"}]},"strategy":"deployment"},"keywords":["prometheus","monitoring","tsdb","alerting"],"labels":{"alm-owner-prometheus":"prometheusoperator","alm-status-descriptors":"prometheusoperator.0.22.2"},"links":[{"name":"Prometheus","url":"https://www.prometheus.io/"},{"name":"Documentation","url":"https://coreos.com/operators/prometheus/docs/latest/"},{"name":"Prometheus Operator","url":"https://github.com/coreos/prometheus-operator"}],"maintainers":[{"email":"openshift-operators@redhat.com","name":"Red Hat"}],"maturity":"beta","provider":{"name":"Red Hat"},"replaces":"prometheusoperator.0.15.0","selector":{"matchLabels":{"alm-owner-prometheus":"prometheusoperator"}},"version":"0.22.2"}}`
	prometheusWithLabelCSVJSON = `{"apiVersion":"operators.coreos.com/v1alpha1","kind":"ClusterServiceVersion","metadata":{"labels": {"test": "label"}, "annotations":{"alm-examples":"[{\"apiVersion\":\"monitoring.coreos.com/v1\",\"kind\":\"Prometheus\",\"metadata\":{\"name\":\"example\",\"labels\":{\"prometheus\":\"k8s\"}},\"spec\":{\"replicas\":2,\"version\":\"v2.3.2\",\"serviceAccountName\":\"prometheus-k8s\",\"securityContext\": {}, \"serviceMonitorSelector\":{\"matchExpressions\":[{\"key\":\"k8s-app\",\"operator\":\"Exists\"}]},\"ruleSelector\":{\"matchLabels\":{\"role\":\"prometheus-rulefiles\",\"prometheus\":\"k8s\"}},\"alerting\":{\"alertmanagers\":[{\"namespace\":\"monitoring\",\"name\":\"alertmanager-main\",\"port\":\"web\"}]}}},{\"apiVersion\":\"monitoring.coreos.com/v1\",\"kind\":\"ServiceMonitor\",\"metadata\":{\"name\":\"example\",\"labels\":{\"k8s-app\":\"prometheus\"}},\"spec\":{\"selector\":{\"matchLabels\":{\"k8s-app\":\"prometheus\"}},\"endpoints\":[{\"port\":\"web\",\"interval\":\"30s\"}]}},{\"apiVersion\":\"monitoring.coreos.com/v1\",\"kind\":\"Alertmanager\",\"metadata\":{\"name\":\"alertmanager-main\"},\"spec\":{\"replicas\":3, \"securityContext\": {}}}]"},"name":"prometheusoperator.0.22.2","namespace":"placeholder"},"spec":{"customresourcedefinitions":{"owned":[{"description":"A running Prometheus instance","displayName":"Prometheus","kind":"Prometheus","name":"prometheuses.monitoring.coreos.com","resources":[{"kind":"StatefulSet","version":"v1beta2"},{"kind":"Pod","version":"v1"}],"specDescriptors":[{"description":"Desired number of Pods for the cluster","displayName":"Size","path":"replicas","x-descriptors":["urn:alm:descriptor:com.tectonic.ui:podCount"]},{"description":"A selector for the ConfigMaps from which to load rule files","displayName":"Rule Config Map Selector","path":"ruleSelector","x-descriptors":["urn:alm:descriptor:com.tectonic.ui:selector:core:v1:ConfigMap"]},{"description":"ServiceMonitors to be selected for target discovery","displayName":"Service Monitor Selector","path":"serviceMonitorSelector","x-descriptors":["urn:alm:descriptor:com.tectonic.ui:selector:monitoring.coreos.com:v1:ServiceMonitor"]},{"description":"The ServiceAccount to use to run the Prometheus pods","displayName":"Service Account","path":"serviceAccountName","x-descriptors":["urn:alm:descriptor:io.kubernetes:ServiceAccount"]},{"description":"Limits describes the minimum/maximum amount of compute resources required/allowed","displayName":"Resource Requirements","path":"resources","x-descriptors":["urn:alm:descriptor:com.tectonic.ui:resourceRequirements"]}],"version":"v1"},{"description":"A Prometheus Rule configures groups of sequentially evaluated recording and alerting rules.","displayName":"Prometheus Rule","kind":"PrometheusRule","name":"prometheusrules.monitoring.coreos.com","version":"v1"},{"description":"Configures prometheus to monitor a particular k8s service","displayName":"Service Monitor","kind":"ServiceMonitor","name":"servicemonitors.monitoring.coreos.com","resources":[{"kind":"Pod","version":"v1"}],"specDescriptors":[{"description":"The label to use to retrieve the job name from","displayName":"Job Label","path":"jobLabel","x-descriptors":["urn:alm:descriptor:com.tectonic.ui:label"]},{"description":"A list of endpoints allowed as part of this ServiceMonitor","displayName":"Endpoints","path":"endpoints","x-descriptors":["urn:alm:descriptor:com.tectonic.ui:endpointList"]}],"version":"v1"},{"description":"Configures an Alertmanager for the namespace","displayName":"Alertmanager","kind":"Alertmanager","name":"alertmanagers.monitoring.coreos.com","resources":[{"kind":"StatefulSet","version":"v1beta2"},{"kind":"Pod","version":"v1"}],"specDescriptors":[{"description":"Desired number of Pods for the cluster","displayName":"Size","path":"replicas","x-descriptors":["urn:alm:descriptor:com.tectonic.ui:podCount"]},{"description":"Limits describes the minimum/maximum amount of compute resources required/allowed","displayName":"Resource Requirements","path":"resources","x-descriptors":["urn:alm:descriptor:com.tectonic.ui:resourceRequirements"]}],"version":"v1"}]},"description":"The Prometheus Operator for Kubernetes provides easy monitoring definitions for Kubernetes services and deployment and management of Prometheus instances.\n\nOnce installed, the Prometheus Operator provides the following features:\n\n* **Create/Destroy**: Easily launch a Prometheus instance for your Kubernetes namespace, a specific application or team easily using the Operator.\n\n* **Simple Configuration**: Configure the fundamentals of Prometheus like versions, persistence, retention policies, and replicas from a native Kubernetes resource.\n\n* **Target Services via Labels**: Automatically generate monitoring target configurations based on familiar Kubernetes label queries; no need to learn a Prometheus specific configuration language.\n\n### Other Supported Features\n\n**High availability**\n\nMultiple instances are run across failure zones and data is replicated. This keeps your monitoring available during an outage, when you need it most.\n\n**Updates via automated operations**\n\nNew Prometheus versions are deployed using a rolling update with no downtime, making it easy to stay up to date.\n\n**Handles the dynamic nature of containers**\n\nAlerting rules are attached to groups of containers instead of individual instances, which is ideal for the highly dynamic nature of container deployment.\n","displayName":"Prometheus Operator","icon":[{"base64data":"PHN2ZyB3aWR0aD0iMjQ5MCIgaGVpZ2h0PSIyNTAwIiB2aWV3Qm94PSIwIDAgMjU2IDI1NyIgeG1sbnM9Imh0dHA6Ly93d3cudzMub3JnLzIwMDAvc3ZnIiBwcmVzZXJ2ZUFzcGVjdFJhdGlvPSJ4TWlkWU1pZCI+PHBhdGggZD0iTTEyOC4wMDEuNjY3QzU3LjMxMS42NjcgMCA1Ny45NzEgMCAxMjguNjY0YzAgNzAuNjkgNTcuMzExIDEyNy45OTggMTI4LjAwMSAxMjcuOTk4UzI1NiAxOTkuMzU0IDI1NiAxMjguNjY0QzI1NiA1Ny45NyAxOTguNjg5LjY2NyAxMjguMDAxLjY2N3ptMCAyMzkuNTZjLTIwLjExMiAwLTM2LjQxOS0xMy40MzUtMzYuNDE5LTMwLjAwNGg3Mi44MzhjMCAxNi41NjYtMTYuMzA2IDMwLjAwNC0zNi40MTkgMzAuMDA0em02MC4xNTMtMzkuOTRINjcuODQyVjE3OC40N2gxMjAuMzE0djIxLjgxNmgtLjAwMnptLS40MzItMzMuMDQ1SDY4LjE4NWMtLjM5OC0uNDU4LS44MDQtLjkxLTEuMTg4LTEuMzc1LTEyLjMxNS0xNC45NTQtMTUuMjE2LTIyLjc2LTE4LjAzMi0zMC43MTYtLjA0OC0uMjYyIDE0LjkzMyAzLjA2IDI1LjU1NiA1LjQ1IDAgMCA1LjQ2NiAxLjI2NSAxMy40NTggMi43MjItNy42NzMtOC45OTQtMTIuMjMtMjAuNDI4LTEyLjIzLTMyLjExNiAwLTI1LjY1OCAxOS42OC00OC4wNzkgMTIuNTgtNjYuMjAxIDYuOTEuNTYyIDE0LjMgMTQuNTgzIDE0LjggMzYuNTA1IDcuMzQ2LTEwLjE1MiAxMC40Mi0yOC42OSAxMC40Mi00MC4wNTYgMC0xMS43NjkgNy43NTUtMjUuNDQgMTUuNTEyLTI1LjkwNy02LjkxNSAxMS4zOTYgMS43OSAyMS4xNjUgOS41MyA0NS40IDIuOTAyIDkuMTAzIDIuNTMyIDI0LjQyMyA0Ljc3MiAzNC4xMzguNzQ0LTIwLjE3OCA0LjIxMy00OS42MiAxNy4wMTQtNTkuNzg0LTUuNjQ3IDEyLjguODM2IDI4LjgxOCA1LjI3IDM2LjUxOCA3LjE1NCAxMi40MjQgMTEuNDkgMjEuODM2IDExLjQ5IDM5LjYzOCAwIDExLjkzNi00LjQwNyAyMy4xNzMtMTEuODQgMzEuOTU4IDguNDUyLTEuNTg2IDE0LjI4OS0zLjAxNiAxNC4yODktMy4wMTZsMjcuNDUtNS4zNTVjLjAwMi0uMDAyLTMuOTg3IDE2LjQwMS0xOS4zMTQgMzIuMTk3eiIgZmlsbD0iI0RBNEUzMSIvPjwvc3ZnPg==","mediatype":"image/svg+xml"}],"install":{"spec":{"deployments":[{"name":"prometheus-operator","spec":{"replicas":1,"selector":{"matchLabels":{"k8s-app":"prometheus-operator"}},"template":{"metadata":{"labels":{"k8s-app":"prometheus-operator"}},"spec":{"containers":[{"args":["-namespace=$(K8S_NAMESPACE)","-manage-crds=false","-logtostderr=true","--config-reloader-image=quay.io/coreos/configmap-reload:v0.0.1","--prometheus-config-reloader=quay.io/coreos/prometheus-config-reloader:v0.22.2"],"env":[{"name":"K8S_NAMESPACE","valueFrom":{"fieldRef":{"fieldPath":"metadata.namespace"}}}],"image":"quay.io/coreos/prometheus-operator@sha256:3daa69a8c6c2f1d35dcf1fe48a7cd8b230e55f5229a1ded438f687debade5bcf","name":"prometheus-operator","ports":[{"containerPort":8080,"name":"http"}],"resources":{"limits":{"cpu":"200m","memory":"100Mi"},"requests":{"cpu":"100m","memory":"50Mi"}},"securityContext":{"allowPrivilegeEscalation":false,"readOnlyRootFilesystem":true}}],"nodeSelector":{"kubernetes.io/os":"linux"},"serviceAccount":"prometheus-operator-0-22-2"}}}}],"permissions":[{"rules":[{"apiGroups":[""],"resources":["nodes","services","endpoints","pods"],"verbs":["get","list","watch"]},{"apiGroups":[""],"resources":["configmaps"],"verbs":["get"]}],"serviceAccountName":"prometheus-k8s"},{"rules":[{"apiGroups":["apiextensions.k8s.io"],"resources":["customresourcedefinitions"],"verbs":["*"]},{"apiGroups":["monitoring.coreos.com"],"resources":["alertmanagers","prometheuses","prometheuses/finalizers","alertmanagers/finalizers","servicemonitors","prometheusrules"],"verbs":["*"]},{"apiGroups":["apps"],"resources":["statefulsets"],"verbs":["*"]},{"apiGroups":[""],"resources":["configmaps","secrets"],"verbs":["*"]},{"apiGroups":[""],"resources":["pods"],"verbs":["list","delete"]},{"apiGroups":[""],"resources":["services","endpoints"],"verbs":["get","create","update"]},{"apiGroups":[""],"resources":["nodes"],"verbs":["list","watch"]},{"apiGroups":[""],"resources":["namespaces"],"verbs":["list","watch"]}],"serviceAccountName":"prometheus-operator-0-22-2"}]},"strategy":"deployment"},"keywords":["prometheus","monitoring","tsdb","alerting"],"labels":{"alm-owner-prometheus":"prometheusoperator","alm-status-descriptors":"prometheusoperator.0.22.2"},"links":[{"name":"Prometheus","url":"https://www.prometheus.io/"},{"name":"Documentation","url":"https://coreos.com/operators/prometheus/docs/latest/"},{"name":"Prometheus Operator","url":"https://github.com/coreos/prometheus-operator"}],"maintainers":[{"email":"openshift-operators@redhat.com","name":"Red Hat"}],"maturity":"beta","provider":{"name":"Red Hat"},"replaces":"prometheusoperator.0.15.0","selector":{"matchLabels":{"alm-owner-prometheus":"prometheusoperator"}},"version":"0.22.2"}}`
)

func TestToPackageManifest(t *testing.T) {
	tests := []struct {
		name          string
		apiPkg        *api.Package
		catalogSource *operatorsv1alpha1.CatalogSource
		bundle        *api.Bundle
		expectedErr   string
		expected      *operators.PackageManifest
	}{
		{
			name: "GoodBundle",
			apiPkg: &api.Package{
				Name: "etcd",
				Channels: []*api.Channel{
					{
						Name:    "alpha",
						CsvName: "etcdoperator.v0.9.2",
					},
				},
				DefaultChannelName: "alpha",
			},
			catalogSource: catalogSource("cool-operators", "ns"),
			bundle: &api.Bundle{
				CsvName:     "etcdoperator.v0.9.2",
				PackageName: "etcd",
				ChannelName: "alpha",
				CsvJson:     etcdCSVJSON,
				Object: []string{
					etcdCSVJSON,
					etcdBackupsCRDJSON,
					etcdUpgradesCRDJSON,
					etcdRestoresCRDJSON,
				},
			},
			expectedErr: "",
			expected: &operators.PackageManifest{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: "ns",
					Labels: labels.Set{
						"catalog":                         "cool-operators",
						"catalog-namespace":               "ns",
						"provider":                        "CoreOS, Inc",
						"provider-url":                    "",
						"operatorframework.io/arch.amd64": "supported",
						"operatorframework.io/os.linux":   "supported",
					},
				},
				Status: operators.PackageManifestStatus{
					CatalogSource:          "cool-operators",
					CatalogSourceNamespace: "ns",
					PackageName:            "etcd",
					Provider: operators.AppLink{
						Name: "CoreOS, Inc",
					},
					DefaultChannel: "alpha",
					Channels: []operators.PackageChannel{
						{
							Name:       "alpha",
							CurrentCSV: "etcdoperator.v0.9.2",
							CurrentCSVDesc: func() operators.CSVDescription {
								csv := operatorsv1alpha1.ClusterServiceVersion{}
								require.NoError(t, json.Unmarshal([]byte(etcdCSVJSON), &csv))
								return operators.CreateCSVDescription(&csv, etcdCSVJSON)
							}(),
						},
					},
				},
			},
		},
		{
			name: "GoodBundle/ExtraLabels",
			apiPkg: &api.Package{
				Name: "etcd",
				Channels: []*api.Channel{
					{
						Name:    "alpha",
						CsvName: "etcdoperator.v0.9.2",
					},
				},
				DefaultChannelName: "alpha",
			},
			catalogSource: catalogSource("cool-operators", "ns"),
			bundle: &api.Bundle{
				CsvName:     "etcdoperator.v0.9.2",
				PackageName: "etcd",
				ChannelName: "alpha",
				CsvJson:     etcdWithLabelsCSVJSON,
				Object: []string{
					etcdWithLabelsCSVJSON,
					etcdBackupsCRDJSON,
					etcdUpgradesCRDJSON,
					etcdRestoresCRDJSON,
				},
			},
			expectedErr: "",
			expected: &operators.PackageManifest{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: "ns",
					Labels: labels.Set{
						"test":                            "label",
						"catalog":                         "cool-operators",
						"catalog-namespace":               "ns",
						"provider":                        "CoreOS, Inc",
						"provider-url":                    "",
						"operatorframework.io/arch.amd64": "supported",
						"operatorframework.io/os.linux":   "supported",
					},
				},
				Status: operators.PackageManifestStatus{
					CatalogSource:          "cool-operators",
					CatalogSourceNamespace: "ns",
					PackageName:            "etcd",
					Provider: operators.AppLink{
						Name: "CoreOS, Inc",
					},
					DefaultChannel: "alpha",
					Channels: []operators.PackageChannel{
						{
							Name:       "alpha",
							CurrentCSV: "etcdoperator.v0.9.2",
							CurrentCSVDesc: func() operators.CSVDescription {
								csv := operatorsv1alpha1.ClusterServiceVersion{}
								require.NoError(t, json.Unmarshal([]byte(etcdCSVJSON), &csv))
								return operators.CreateCSVDescription(&csv, etcdCSVJSON)
							}(),
						},
					},
				},
			},
		},
		{
			name: "MissingBundle/ChannelElided",
			apiPkg: &api.Package{
				Name: "etcd",
				Channels: []*api.Channel{
					{
						Name:    "alpha",
						CsvName: "etcdoperator.v0.9.2",
					},
					{
						Name:    "missing-bundle",
						CsvName: "etcdoperator.v0.9.1",
					},
				},
				DefaultChannelName: "alpha",
			},
			catalogSource: catalogSource("cool-operators", "ns"),
			bundle: &api.Bundle{
				CsvName:     "etcdoperator.v0.9.2",
				PackageName: "etcd",
				ChannelName: "alpha",
				CsvJson:     etcdCSVJSON,
				Object: []string{
					etcdCSVJSON,
					etcdBackupsCRDJSON,
					etcdUpgradesCRDJSON,
					etcdRestoresCRDJSON,
				},
			},
			expectedErr: "",
			expected: &operators.PackageManifest{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: "ns",
					Labels: labels.Set{
						"catalog":                         "cool-operators",
						"catalog-namespace":               "ns",
						"provider":                        "CoreOS, Inc",
						"provider-url":                    "",
						"operatorframework.io/arch.amd64": "supported",
						"operatorframework.io/os.linux":   "supported",
					},
				},
				Status: operators.PackageManifestStatus{
					CatalogSource:          "cool-operators",
					CatalogSourceNamespace: "ns",
					PackageName:            "etcd",
					Provider: operators.AppLink{
						Name: "CoreOS, Inc",
					},
					DefaultChannel: "alpha",
					Channels: []operators.PackageChannel{
						{
							Name:       "alpha",
							CurrentCSV: "etcdoperator.v0.9.2",
							CurrentCSVDesc: func() operators.CSVDescription {
								csv := operatorsv1alpha1.ClusterServiceVersion{}
								require.NoError(t, json.Unmarshal([]byte(etcdCSVJSON), &csv))
								return operators.CreateCSVDescription(&csv, etcdCSVJSON)
							}(),
						},
					},
				},
			},
		},
		{
			name: "MissingBundle/DefaultChannelElided",
			apiPkg: &api.Package{
				Name: "etcd",
				Channels: []*api.Channel{
					{
						Name:    "alpha",
						CsvName: "etcdoperator.v0.9.2",
					},
					{
						Name:    "missing-bundle",
						CsvName: "etcdoperator.v0.9.1",
					},
				},
				DefaultChannelName: "missing-bundle",
			},
			catalogSource: catalogSource("cool-operators", "ns"),
			bundle: &api.Bundle{
				CsvName:     "etcdoperator.v0.9.2",
				PackageName: "etcd",
				ChannelName: "alpha",
				CsvJson:     etcdCSVJSON,
				Object: []string{
					etcdCSVJSON,
					etcdBackupsCRDJSON,
					etcdUpgradesCRDJSON,
					etcdRestoresCRDJSON,
				},
			},
			expectedErr: "",
			expected: &operators.PackageManifest{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: "ns",
					Labels: labels.Set{
						"catalog":                         "cool-operators",
						"catalog-namespace":               "ns",
						"provider":                        "CoreOS, Inc",
						"provider-url":                    "",
						"operatorframework.io/arch.amd64": "supported",
						"operatorframework.io/os.linux":   "supported",
					},
				},
				Status: operators.PackageManifestStatus{
					CatalogSource:          "cool-operators",
					CatalogSourceNamespace: "ns",
					PackageName:            "etcd",
					Provider: operators.AppLink{
						Name: "CoreOS, Inc",
					},
					DefaultChannel: "alpha",
					Channels: []operators.PackageChannel{
						{
							Name:       "alpha",
							CurrentCSV: "etcdoperator.v0.9.2",
							CurrentCSVDesc: func() operators.CSVDescription {
								csv := operatorsv1alpha1.ClusterServiceVersion{}
								require.NoError(t, json.Unmarshal([]byte(etcdCSVJSON), &csv))
								return operators.CreateCSVDescription(&csv, etcdCSVJSON)
							}(),
						},
					},
				},
			},
		},
		{
			name: "NoBundles/NoValidChannels",
			apiPkg: &api.Package{
				Name: "etcd",
				Channels: []*api.Channel{
					{
						Name:    "alpha",
						CsvName: "etcdoperator.v0.9.2",
					},
				},
				DefaultChannelName: "alpha",
			},
			catalogSource: catalogSource("cool-operators", "ns"),
			bundle: &api.Bundle{
				CsvName:     "etcdoperator.v0.9.2",
				PackageName: "etcd",
				ChannelName: "alpha",
			},
			expectedErr: "packagemanifest has no valid channels",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clientFake := &fakes.FakeRegistryClient{}
			clientFake.GetBundleForChannelReturnsOnCall(0, test.bundle, nil)

			client := &registryClient{
				RegistryClient: clientFake,
				catsrc:         test.catalogSource,
			}

			packageManifest, err := newPackageManifest(context.Background(), logrus.NewEntry(logrus.New()), test.apiPkg, client)
			if test.expectedErr != "" {
				require.Error(t, err)
				require.Equal(t, test.expectedErr, err.Error())
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, test.expected, packageManifest)
		})
	}
}

func TestRegistryProviderGet(t *testing.T) {
	type getRequest struct {
		packageNamespace string
		packageName      string
	}
	tests := []struct {
		name           string
		namespaces     []string
		globalNS       string
		catalogSources []runtime.Object
		request        getRequest
		expectedErr    string
		expected       *operators.PackageManifest
	}{
		{
			name:       "SingleNamespace/PackageManifestNotFound",
			namespaces: []string{"ns"},
			globalNS:   "ns",
			catalogSources: []runtime.Object{
				withRegistryServiceStatus(catalogSource("cool-operators", "ns"), "grpc", "cool-operators", "ns", port, metav1.NewTime(time.Now())),
			},
			request: getRequest{
				packageNamespace: "ns",
				packageName:      "amq",
			},
			expectedErr: "",
			expected:    nil,
		},
		{
			name:       "SingleNamespace/PackageManifestFound",
			namespaces: []string{"ns"},
			globalNS:   "ns",
			catalogSources: []runtime.Object{
				withRegistryServiceStatus(catalogSource("cool-operators", "ns"), "grpc", "cool-operators", "ns", port, metav1.NewTime(time.Now())),
			},
			request: getRequest{
				// A package known to exist in the directory-loaded registry.
				packageNamespace: "ns",
				packageName:      "etcd",
			},
			expectedErr: "",
			expected: &operators.PackageManifest{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: "ns",
					Labels: labels.Set{
						"catalog":                         "cool-operators",
						"catalog-namespace":               "ns",
						"provider":                        "CoreOS, Inc",
						"provider-url":                    "",
						"operatorframework.io/arch.amd64": "supported",
						"operatorframework.io/os.linux":   "supported",
					},
				},
				Status: operators.PackageManifestStatus{
					CatalogSource:          "cool-operators",
					CatalogSourceNamespace: "ns",
					PackageName:            "etcd",
					Provider: operators.AppLink{
						Name: "CoreOS, Inc",
					},
					DefaultChannel: "alpha",
					Channels: []operators.PackageChannel{
						{
							Name:       "alpha",
							CurrentCSV: "etcdoperator.v0.9.2",
							CurrentCSVDesc: func() operators.CSVDescription {
								csv := operatorsv1alpha1.ClusterServiceVersion{}
								require.NoError(t, json.Unmarshal([]byte(etcdCSVJSON), &csv))
								return operators.CreateCSVDescription(&csv, etcdCSVJSON)
							}(),
						},
					},
				},
			},
		},
		{
			name:       "SingleNamespace/TwoCatalogs/OneBadConnection/PackageManifestFound",
			namespaces: []string{"ns"},
			globalNS:   "ns",
			catalogSources: []runtime.Object{
				withRegistryServiceStatus(catalogSource("cool-operators", "ns"), "grpc", "cool-operators", "ns", port, metav1.NewTime(time.Now())),
				withRegistryServiceStatus(catalogSource("not-so-cool-operators", "ns"), "grpc", "not-so-cool-operators", "ns", "50052", metav1.NewTime(time.Now())),
			},
			request: getRequest{
				// A package known to exist in the directory-loaded registry.
				packageNamespace: "ns",
				packageName:      "etcd",
			},
			expectedErr: "",
			expected: &operators.PackageManifest{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: "ns",
					Labels: labels.Set{
						"catalog":                         "cool-operators",
						"catalog-namespace":               "ns",
						"provider":                        "CoreOS, Inc",
						"provider-url":                    "",
						"operatorframework.io/arch.amd64": "supported",
						"operatorframework.io/os.linux":   "supported",
					},
				},
				Status: operators.PackageManifestStatus{
					CatalogSource:          "cool-operators",
					CatalogSourceNamespace: "ns",
					PackageName:            "etcd",
					Provider: operators.AppLink{
						Name: "CoreOS, Inc",
					},
					DefaultChannel: "alpha",
					Channels: []operators.PackageChannel{
						{
							Name:       "alpha",
							CurrentCSV: "etcdoperator.v0.9.2",
							CurrentCSVDesc: func() operators.CSVDescription {
								csv := operatorsv1alpha1.ClusterServiceVersion{}
								require.NoError(t, json.Unmarshal([]byte(etcdCSVJSON), &csv))
								return operators.CreateCSVDescription(&csv, etcdCSVJSON)
							}(),
						},
					},
				},
			},
		},
		{
			name:       "GlobalNamespace/PackageManifestFound",
			namespaces: []string{"ns", "global"},
			globalNS:   "global",
			catalogSources: []runtime.Object{
				withRegistryServiceStatus(catalogSource("cool-operators", "global"), "grpc", "cool-operators", "global", port, metav1.NewTime(time.Now())),
			},
			request: getRequest{
				// A package known to exist in the directory-loaded registry.
				packageNamespace: "ns",
				packageName:      "etcd",
			},
			expectedErr: "",
			expected: &operators.PackageManifest{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: "ns",
					Labels: labels.Set{
						"catalog":                         "cool-operators",
						"catalog-namespace":               "global",
						"provider":                        "CoreOS, Inc",
						"provider-url":                    "",
						"operatorframework.io/arch.amd64": "supported",
						"operatorframework.io/os.linux":   "supported",
					},
				},
				Status: operators.PackageManifestStatus{
					CatalogSource:          "cool-operators",
					CatalogSourceNamespace: "global",
					PackageName:            "etcd",
					Provider: operators.AppLink{
						Name: "CoreOS, Inc",
					},
					DefaultChannel: "alpha",
					Channels: []operators.PackageChannel{
						{
							Name:       "alpha",
							CurrentCSV: "etcdoperator.v0.9.2",
							CurrentCSVDesc: func() operators.CSVDescription {
								csv := operatorsv1alpha1.ClusterServiceVersion{}
								require.NoError(t, json.Unmarshal([]byte(etcdCSVJSON), &csv))
								return operators.CreateCSVDescription(&csv, etcdCSVJSON)
							}(),
						},
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()
			provider, err := NewFakeRegistryProvider(ctx, nil, nil, test.globalNS)
			require.NoError(t, err)

			for _, cs := range test.catalogSources {
				catsrc := cs.(*operatorsv1alpha1.CatalogSource)
				require.NoError(t, provider.refreshCache(ctx, newTestRegistryClient(t, catsrc)))
			}

			packageManifest, err := provider.Get(test.request.packageNamespace, test.request.packageName)
			if test.expectedErr != "" {
				require.NotNil(t, err)
				require.Equal(t, test.expectedErr, err.Error())
			} else {
				require.Nil(t, err)
			}

			require.EqualValues(t, test.expected, packageManifest)
		})
	}
}

func TestRegistryProviderList(t *testing.T) {
	tests := []struct {
		name             string
		globalNS         string
		registryClients  []*registryClient
		requestNamespace string
		expectedErr      string
		expected         *operators.PackageManifestList
	}{
		{
			name:             "NoPackages",
			globalNS:         "ns",
			requestNamespace: "wisconsin",
			expectedErr:      "",
			expected:         &operators.PackageManifestList{Items: []operators.PackageManifest{}},
		},
		{
			name:     "PackagesFound",
			globalNS: "ns",
			registryClients: []*registryClient{
				newTestRegistryClient(t, withRegistryServiceStatus(catalogSource("cool-operators", "ns"), "grpc", "cool-operators", "ns", port, metav1.NewTime(time.Now()))),
			},
			requestNamespace: "ns",
			expectedErr:      "",
			expected: &operators.PackageManifestList{Items: []operators.PackageManifest{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus",
						Namespace: "ns",
						Labels: labels.Set{
							"catalog":                         "cool-operators",
							"catalog-namespace":               "ns",
							"provider":                        "Red Hat",
							"provider-url":                    "",
							"operatorframework.io/arch.amd64": "supported",
							"operatorframework.io/os.linux":   "supported",
						},
					},
					Status: operators.PackageManifestStatus{
						CatalogSource:          "cool-operators",
						CatalogSourceNamespace: "ns",
						PackageName:            "prometheus",
						Provider: operators.AppLink{
							Name: "Red Hat",
						},
						DefaultChannel: "preview",
						Channels: []operators.PackageChannel{
							{
								Name:       "preview",
								CurrentCSV: "prometheusoperator.0.22.2",
								CurrentCSVDesc: func() operators.CSVDescription {
									csv := operatorsv1alpha1.ClusterServiceVersion{}
									require.NoError(t, json.Unmarshal([]byte(prometheusCSVJSON), &csv))
									return operators.CreateCSVDescription(&csv, prometheusCSVJSON)
								}(),
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd",
						Namespace: "ns",
						Labels: labels.Set{
							"catalog":                         "cool-operators",
							"catalog-namespace":               "ns",
							"provider":                        "CoreOS, Inc",
							"provider-url":                    "",
							"operatorframework.io/arch.amd64": "supported",
							"operatorframework.io/os.linux":   "supported",
						},
					},
					Status: operators.PackageManifestStatus{
						CatalogSource:          "cool-operators",
						CatalogSourceNamespace: "ns",
						PackageName:            "etcd",
						Provider: operators.AppLink{
							Name: "CoreOS, Inc",
						},
						DefaultChannel: "alpha",
						Channels: []operators.PackageChannel{
							{
								Name:       "alpha",
								CurrentCSV: "etcdoperator.v0.9.2",
								CurrentCSVDesc: func() operators.CSVDescription {
									csv := operatorsv1alpha1.ClusterServiceVersion{}
									require.NoError(t, json.Unmarshal([]byte(etcdCSVJSON), &csv))
									return operators.CreateCSVDescription(&csv, etcdCSVJSON)
								}(),
							},
						},
					},
				},
			}},
		},
		{
			name:     "TwoCatalogs/OneBadConnection/PackagesFound",
			globalNS: "ns",
			registryClients: []*registryClient{
				newTestRegistryClient(t, withRegistryServiceStatus(catalogSource("cool-operators", "ns"), "grpc", "cool-operators", "ns", port, metav1.NewTime(time.Now()))),
				newTestRegistryClient(t, withRegistryServiceStatus(catalogSource("not-so-cool-operators", "ns"), "grpc", "not-so-cool-operators", "ns", "50052", metav1.NewTime(time.Now()))),
			},
			requestNamespace: "ns",
			expectedErr:      "",
			expected: &operators.PackageManifestList{Items: []operators.PackageManifest{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus",
						Namespace: "ns",
						Labels: labels.Set{
							"catalog":                         "cool-operators",
							"catalog-namespace":               "ns",
							"provider":                        "Red Hat",
							"provider-url":                    "",
							"operatorframework.io/arch.amd64": "supported",
							"operatorframework.io/os.linux":   "supported",
						},
					},
					Status: operators.PackageManifestStatus{
						CatalogSource:          "cool-operators",
						CatalogSourceNamespace: "ns",
						PackageName:            "prometheus",
						Provider: operators.AppLink{
							Name: "Red Hat",
						},
						DefaultChannel: "preview",
						Channels: []operators.PackageChannel{
							{
								Name:       "preview",
								CurrentCSV: "prometheusoperator.0.22.2",
								CurrentCSVDesc: func() operators.CSVDescription {
									csv := operatorsv1alpha1.ClusterServiceVersion{}
									require.NoError(t, json.Unmarshal([]byte(prometheusCSVJSON), &csv))
									return operators.CreateCSVDescription(&csv, prometheusCSVJSON)
								}(),
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd",
						Namespace: "ns",
						Labels: labels.Set{
							"catalog":                         "cool-operators",
							"catalog-namespace":               "ns",
							"provider":                        "CoreOS, Inc",
							"provider-url":                    "",
							"operatorframework.io/arch.amd64": "supported",
							"operatorframework.io/os.linux":   "supported",
						},
					},
					Status: operators.PackageManifestStatus{
						CatalogSource:          "cool-operators",
						CatalogSourceNamespace: "ns",
						PackageName:            "etcd",
						Provider: operators.AppLink{
							Name: "CoreOS, Inc",
						},
						DefaultChannel: "alpha",
						Channels: []operators.PackageChannel{
							{
								Name:       "alpha",
								CurrentCSV: "etcdoperator.v0.9.2",
								CurrentCSVDesc: func() operators.CSVDescription {
									csv := operatorsv1alpha1.ClusterServiceVersion{}
									require.NoError(t, json.Unmarshal([]byte(etcdCSVJSON), &csv))
									return operators.CreateCSVDescription(&csv, etcdCSVJSON)
								}(),
							},
						},
					},
				},
			}},
		},
		{
			name:     "TwoCatalogs/SameNamespace/DuplicatePackages/PackagesFound",
			globalNS: "ns",
			registryClients: []*registryClient{
				newTestRegistryClient(t, withRegistryServiceStatus(catalogSource("cool-operators", "ns"), "grpc", "cool-operators", "ns", port, metav1.NewTime(time.Now()))),
				newTestRegistryClient(t, withRegistryServiceStatus(catalogSource("cool-operators-2", "ns"), "grpc", "cool-operators-2", "ns", port, metav1.NewTime(time.Now()))),
			},
			requestNamespace: "ns",
			expectedErr:      "",
			expected: &operators.PackageManifestList{Items: []operators.PackageManifest{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus",
						Namespace: "ns",
						Labels: labels.Set{
							"catalog":                         "cool-operators",
							"catalog-namespace":               "ns",
							"provider":                        "Red Hat",
							"provider-url":                    "",
							"operatorframework.io/arch.amd64": "supported",
							"operatorframework.io/os.linux":   "supported",
						},
					},
					Status: operators.PackageManifestStatus{
						CatalogSource:          "cool-operators",
						CatalogSourceNamespace: "ns",
						PackageName:            "prometheus",
						Provider: operators.AppLink{
							Name: "Red Hat",
						},
						DefaultChannel: "preview",
						Channels: []operators.PackageChannel{
							{
								Name:       "preview",
								CurrentCSV: "prometheusoperator.0.22.2",
								CurrentCSVDesc: func() operators.CSVDescription {
									csv := operatorsv1alpha1.ClusterServiceVersion{}
									require.NoError(t, json.Unmarshal([]byte(prometheusCSVJSON), &csv))
									return operators.CreateCSVDescription(&csv, prometheusCSVJSON)
								}(),
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd",
						Namespace: "ns",
						Labels: labels.Set{
							"catalog":                         "cool-operators",
							"catalog-namespace":               "ns",
							"provider":                        "CoreOS, Inc",
							"provider-url":                    "",
							"operatorframework.io/arch.amd64": "supported",
							"operatorframework.io/os.linux":   "supported",
						},
					},
					Status: operators.PackageManifestStatus{
						CatalogSource:          "cool-operators",
						CatalogSourceNamespace: "ns",
						PackageName:            "etcd",
						Provider: operators.AppLink{
							Name: "CoreOS, Inc",
						},
						DefaultChannel: "alpha",
						Channels: []operators.PackageChannel{
							{
								Name:       "alpha",
								CurrentCSV: "etcdoperator.v0.9.2",
								CurrentCSVDesc: func() operators.CSVDescription {
									csv := operatorsv1alpha1.ClusterServiceVersion{}
									require.NoError(t, json.Unmarshal([]byte(etcdCSVJSON), &csv))
									return operators.CreateCSVDescription(&csv, etcdCSVJSON)
								}(),
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus",
						Namespace: "ns",
						Labels: labels.Set{
							"catalog":                         "cool-operators-2",
							"catalog-namespace":               "ns",
							"provider":                        "Red Hat",
							"provider-url":                    "",
							"operatorframework.io/arch.amd64": "supported",
							"operatorframework.io/os.linux":   "supported",
						},
					},
					Status: operators.PackageManifestStatus{
						CatalogSource:          "cool-operators-2",
						CatalogSourceNamespace: "ns",
						PackageName:            "prometheus",
						Provider: operators.AppLink{
							Name: "Red Hat",
						},
						DefaultChannel: "preview",
						Channels: []operators.PackageChannel{
							{
								Name:       "preview",
								CurrentCSV: "prometheusoperator.0.22.2",
								CurrentCSVDesc: func() operators.CSVDescription {
									csv := operatorsv1alpha1.ClusterServiceVersion{}
									require.NoError(t, json.Unmarshal([]byte(prometheusCSVJSON), &csv))
									return operators.CreateCSVDescription(&csv, prometheusCSVJSON)
								}(),
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd",
						Namespace: "ns",
						Labels: labels.Set{
							"catalog":                         "cool-operators-2",
							"catalog-namespace":               "ns",
							"provider":                        "CoreOS, Inc",
							"provider-url":                    "",
							"operatorframework.io/arch.amd64": "supported",
							"operatorframework.io/os.linux":   "supported",
						},
					},
					Status: operators.PackageManifestStatus{
						CatalogSource:          "cool-operators-2",
						CatalogSourceNamespace: "ns",
						PackageName:            "etcd",
						Provider: operators.AppLink{
							Name: "CoreOS, Inc",
						},
						DefaultChannel: "alpha",
						Channels: []operators.PackageChannel{
							{
								Name:       "alpha",
								CurrentCSV: "etcdoperator.v0.9.2",
								CurrentCSVDesc: func() operators.CSVDescription {
									csv := operatorsv1alpha1.ClusterServiceVersion{}
									require.NoError(t, json.Unmarshal([]byte(etcdCSVJSON), &csv))
									return operators.CreateCSVDescription(&csv, etcdCSVJSON)
								}(),
							},
						},
					},
				},
			}},
		},
		{
			name:     "OneCatalog/ManyPackages/OneMissingBundle/Elided",
			globalNS: "ns",
			registryClients: []*registryClient{
				func() *registryClient {
					catsrc := catalogSource("cool-operators", "ns")
					listFake := &fakes.FakeRegistry_ListPackagesClient{}
					listFake.RecvReturnsOnCall(0, &api.PackageName{Name: "no-bundle"}, nil)
					listFake.RecvReturnsOnCall(1, &api.PackageName{Name: "has-bundle"}, nil)
					listFake.RecvReturnsOnCall(2, nil, io.EOF)

					clientFake := &fakes.FakeRegistryClient{}
					clientFake.ListPackagesReturns(listFake, nil)
					clientFake.GetPackageReturnsOnCall(0, &api.Package{
						Name: "no-bundle",
						Channels: []*api.Channel{
							{
								Name:    "alpha",
								CsvName: "xanthoporessa.v0.0.0",
							},
						},
						DefaultChannelName: "alpha",
					}, nil)
					clientFake.GetPackageReturnsOnCall(1, &api.Package{
						Name: "has-bundle",
						Channels: []*api.Channel{
							{
								Name:    "alpha",
								CsvName: "etcdoperator.v0.9.2",
							},
						},
						DefaultChannelName: "alpha",
					}, nil)
					clientFake.GetBundleForChannelReturnsOnCall(0, nil, fmt.Errorf("no bundle found"))
					clientFake.GetBundleForChannelReturnsOnCall(1, &api.Bundle{
						CsvName:     "etcdoperator.v0.9.2",
						PackageName: "has-bundle",
						ChannelName: "alpha",
						CsvJson:     etcdCSVJSON,
						Object: []string{
							etcdCSVJSON,
							etcdBackupsCRDJSON,
							etcdUpgradesCRDJSON,
							etcdRestoresCRDJSON,
						},
					}, nil)

					return &registryClient{clientFake, catsrc, nil}
				}(),
			},
			requestNamespace: "ns",
			expectedErr:      "",
			expected: &operators.PackageManifestList{Items: []operators.PackageManifest{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "has-bundle",
						Namespace: "ns",
						Labels: labels.Set{
							"catalog":                         "cool-operators",
							"catalog-namespace":               "ns",
							"provider":                        "CoreOS, Inc",
							"provider-url":                    "",
							"operatorframework.io/arch.amd64": "supported",
							"operatorframework.io/os.linux":   "supported",
						},
					},
					Status: operators.PackageManifestStatus{
						CatalogSource:          "cool-operators",
						CatalogSourceNamespace: "ns",
						PackageName:            "has-bundle",
						Provider: operators.AppLink{
							Name: "CoreOS, Inc",
						},
						DefaultChannel: "alpha",
						Channels: []operators.PackageChannel{
							{
								Name:       "alpha",
								CurrentCSV: "etcdoperator.v0.9.2",
								CurrentCSVDesc: func() operators.CSVDescription {
									csv := operatorsv1alpha1.ClusterServiceVersion{}
									require.NoError(t, json.Unmarshal([]byte(etcdCSVJSON), &csv))
									return operators.CreateCSVDescription(&csv, etcdCSVJSON)
								}(),
							},
						},
					},
				},
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()
			provider, err := NewFakeRegistryProvider(ctx, nil, nil, test.globalNS)
			require.NoError(t, err)

			for _, c := range test.registryClients {
				require.NoError(t, provider.refreshCache(ctx, c))
			}

			packageManifestList, err := provider.List(test.requestNamespace, labels.Everything())
			if test.expectedErr != "" {
				require.NotNil(t, err)
				require.Equal(t, test.expectedErr, err.Error())
			} else {
				require.Nil(t, err)
			}

			require.Equal(t, len(test.expected.Items), len(packageManifestList.Items))
			require.ElementsMatch(t, test.expected.Items, packageManifestList.Items)
		})
	}
}

type LabelReq struct {
	key       string
	op        selection.Operator
	strValues []string
}

func TestRegistryProviderListLabels(t *testing.T) {
	tests := []struct {
		name             string
		globalNS         string
		labelReq         *LabelReq
		registryClients  []*registryClient
		requestNamespace string
		expectedErr      string
		expected         *operators.PackageManifestList
	}{
		{
			name:     "PackagesFound/LabelsSupported/SingleNS",
			globalNS: "ns",
			labelReq: &LabelReq{
				key: "catalog",
				op:  selection.Exists,
			},
			registryClients: []*registryClient{
				newTestRegistryClient(t, withRegistryServiceStatus(catalogSource("cool-operators", "ns"), "grpc", "cool-operators", "ns", port, metav1.NewTime(time.Now()))),
			},
			requestNamespace: "ns",
			expectedErr:      "",
			expected: &operators.PackageManifestList{Items: []operators.PackageManifest{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus",
						Namespace: "ns",
						Labels: labels.Set{
							"catalog":                         "cool-operators",
							"catalog-namespace":               "ns",
							"provider":                        "Red Hat",
							"provider-url":                    "",
							"operatorframework.io/arch.amd64": "supported",
							"operatorframework.io/os.linux":   "supported",
						},
					},
					Status: operators.PackageManifestStatus{
						CatalogSource:          "cool-operators",
						CatalogSourceNamespace: "ns",
						PackageName:            "prometheus",
						Provider: operators.AppLink{
							Name: "Red Hat",
						},
						DefaultChannel: "preview",
						Channels: []operators.PackageChannel{
							{
								Name:       "preview",
								CurrentCSV: "prometheusoperator.0.22.2",
								CurrentCSVDesc: func() operators.CSVDescription {
									csv := operatorsv1alpha1.ClusterServiceVersion{}
									require.NoError(t, json.Unmarshal([]byte(prometheusCSVJSON), &csv))
									return operators.CreateCSVDescription(&csv, prometheusCSVJSON)
								}(),
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd",
						Namespace: "ns",
						Labels: labels.Set{
							"catalog":                         "cool-operators",
							"catalog-namespace":               "ns",
							"provider":                        "CoreOS, Inc",
							"provider-url":                    "",
							"operatorframework.io/arch.amd64": "supported",
							"operatorframework.io/os.linux":   "supported",
						},
					},
					Status: operators.PackageManifestStatus{
						CatalogSource:          "cool-operators",
						CatalogSourceNamespace: "ns",
						PackageName:            "etcd",
						Provider: operators.AppLink{
							Name: "CoreOS, Inc",
						},
						DefaultChannel: "alpha",
						Channels: []operators.PackageChannel{
							{
								Name:       "alpha",
								CurrentCSV: "etcdoperator.v0.9.2",
								CurrentCSVDesc: func() operators.CSVDescription {
									csv := operatorsv1alpha1.ClusterServiceVersion{}
									require.NoError(t, json.Unmarshal([]byte(etcdCSVJSON), &csv))
									return operators.CreateCSVDescription(&csv, etcdCSVJSON)
								}(),
							},
						},
					},
				},
			}},
		},
		{
			name:     "PackagesFound/LabelsSupported/GlobalNS",
			globalNS: "ns",
			labelReq: &LabelReq{
				key: "catalog",
				op:  selection.Exists,
			},
			registryClients: []*registryClient{
				newTestRegistryClient(t, withRegistryServiceStatus(catalogSource("cool-operators", "ns"), "grpc", "cool-operators", "ns", port, metav1.NewTime(time.Now()))),
			},
			requestNamespace: "",
			expectedErr:      "",
			expected: &operators.PackageManifestList{Items: []operators.PackageManifest{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus",
						Namespace: "ns",
						Labels: labels.Set{
							"catalog":                         "cool-operators",
							"catalog-namespace":               "ns",
							"provider":                        "Red Hat",
							"provider-url":                    "",
							"operatorframework.io/arch.amd64": "supported",
							"operatorframework.io/os.linux":   "supported",
						},
					},
					Status: operators.PackageManifestStatus{
						CatalogSource:          "cool-operators",
						CatalogSourceNamespace: "ns",
						PackageName:            "prometheus",
						Provider: operators.AppLink{
							Name: "Red Hat",
						},
						DefaultChannel: "preview",
						Channels: []operators.PackageChannel{
							{
								Name:       "preview",
								CurrentCSV: "prometheusoperator.0.22.2",
								CurrentCSVDesc: func() operators.CSVDescription {
									csv := operatorsv1alpha1.ClusterServiceVersion{}
									require.NoError(t, json.Unmarshal([]byte(prometheusCSVJSON), &csv))
									return operators.CreateCSVDescription(&csv, prometheusCSVJSON)
								}(),
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd",
						Namespace: "ns",
						Labels: labels.Set{
							"catalog":                         "cool-operators",
							"catalog-namespace":               "ns",
							"provider":                        "CoreOS, Inc",
							"provider-url":                    "",
							"operatorframework.io/arch.amd64": "supported",
							"operatorframework.io/os.linux":   "supported",
						},
					},
					Status: operators.PackageManifestStatus{
						CatalogSource:          "cool-operators",
						CatalogSourceNamespace: "ns",
						PackageName:            "etcd",
						Provider: operators.AppLink{
							Name: "CoreOS, Inc",
						},
						DefaultChannel: "alpha",
						Channels: []operators.PackageChannel{
							{
								Name:       "alpha",
								CurrentCSV: "etcdoperator.v0.9.2",
								CurrentCSVDesc: func() operators.CSVDescription {
									csv := operatorsv1alpha1.ClusterServiceVersion{}
									require.NoError(t, json.Unmarshal([]byte(etcdCSVJSON), &csv))
									return operators.CreateCSVDescription(&csv, etcdCSVJSON)
								}(),
							},
						},
					},
				},
			}},
		},
		{
			name:     "PackagesNotFound/LabelsNotSupported/GlobalNS",
			globalNS: "",
			labelReq: &LabelReq{
				key: "catalog",
				op:  selection.DoesNotExist,
			},
			registryClients: []*registryClient{
				newTestRegistryClient(t, withRegistryServiceStatus(catalogSource("cool-operators", ""), "grpc", "cool-operators", "ns", port, metav1.NewTime(time.Now()))),
			},
			requestNamespace: "",
			expectedErr:      "",
			expected:         &operators.PackageManifestList{Items: []operators.PackageManifest{}},
		},
		{
			name:     "PackagesFound/LabelsNotProvided/GlobalNS",
			globalNS: "",
			registryClients: []*registryClient{
				newTestRegistryClient(t, withRegistryServiceStatus(catalogSource("cool-operators", "ns"), "grpc", "cool-operators", "ns", port, metav1.NewTime(time.Now()))),
			},
			requestNamespace: "",
			expectedErr:      "",
			expected: &operators.PackageManifestList{Items: []operators.PackageManifest{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus",
						Namespace: "ns",
						Labels: labels.Set{
							"catalog":                         "cool-operators",
							"catalog-namespace":               "ns",
							"provider":                        "Red Hat",
							"provider-url":                    "",
							"operatorframework.io/arch.amd64": "supported",
							"operatorframework.io/os.linux":   "supported",
						},
					},
					Status: operators.PackageManifestStatus{
						CatalogSource:          "cool-operators",
						CatalogSourceNamespace: "ns",
						PackageName:            "prometheus",
						Provider: operators.AppLink{
							Name: "Red Hat",
						},
						DefaultChannel: "preview",
						Channels: []operators.PackageChannel{
							{
								Name:       "preview",
								CurrentCSV: "prometheusoperator.0.22.2",
								CurrentCSVDesc: func() operators.CSVDescription {
									csv := operatorsv1alpha1.ClusterServiceVersion{}
									require.NoError(t, json.Unmarshal([]byte(prometheusCSVJSON), &csv))
									return operators.CreateCSVDescription(&csv, prometheusCSVJSON)
								}(),
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd",
						Namespace: "ns",
						Labels: labels.Set{
							"catalog":                         "cool-operators",
							"catalog-namespace":               "ns",
							"provider":                        "CoreOS, Inc",
							"provider-url":                    "",
							"operatorframework.io/arch.amd64": "supported",
							"operatorframework.io/os.linux":   "supported",
						},
					},
					Status: operators.PackageManifestStatus{
						CatalogSource:          "cool-operators",
						CatalogSourceNamespace: "ns",
						PackageName:            "etcd",
						Provider: operators.AppLink{
							Name: "CoreOS, Inc",
						},
						DefaultChannel: "alpha",
						Channels: []operators.PackageChannel{
							{
								Name:       "alpha",
								CurrentCSV: "etcdoperator.v0.9.2",
								CurrentCSVDesc: func() operators.CSVDescription {
									csv := operatorsv1alpha1.ClusterServiceVersion{}
									require.NoError(t, json.Unmarshal([]byte(etcdCSVJSON), &csv))
									return operators.CreateCSVDescription(&csv, etcdCSVJSON)
								}(),
							},
						},
					},
				},
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			lab := labels.NewSelector()
			if test.labelReq != nil {
				req, err := labels.NewRequirement(test.labelReq.key, test.labelReq.op, test.labelReq.strValues)
				require.NoError(t, err)
				lab = lab.Add(*req)
			}

			provider, err := NewFakeRegistryProvider(ctx, nil, nil, test.globalNS)
			require.NoError(t, err)

			for _, c := range test.registryClients {
				require.NoError(t, provider.refreshCache(ctx, c))
			}

			packageManifestList, err := provider.List(test.requestNamespace, lab)
			if test.expectedErr != "" {
				require.NotNil(t, err)
				require.Equal(t, test.expectedErr, err.Error())
			} else {
				require.Nil(t, err)
			}

			require.Equal(t, len(test.expected.Items), len(packageManifestList.Items))
			require.ElementsMatch(t, test.expected.Items, packageManifestList.Items)
		})
	}
}

func newTestRegistryClient(t *testing.T, catsrc *operatorsv1alpha1.CatalogSource) *registryClient {
	conn, err := grpc.Dial(address+catsrc.Status.RegistryServiceStatus.Port, grpc.WithInsecure())
	require.NoError(t, err, "could not set up test grpc connection")
	return newRegistryClient(catsrc, conn)
}

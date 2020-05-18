package migration

import (
	"bytes"
	ctx "context"
	"fmt"
	migratorv1alpha1 "github.com/kubernetes-sigs/kube-storage-version-migrator/pkg/apis/migration/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
)

// The migration package contains helper functions used by the catalog operator to create StorageVersionMigration objects.
// These objects are handled by the kube-storage-version-migrator and ensure that all CRs pertaining to a given CRD
// are stored in the backend at the latest storage version.

const (
	storageMigrationKind    = "StorageVersionMigration"
	storageMigrationVersion = "v1alpha1"
)

var migratorGVR = schema.GroupVersionResource{
	Group:    migratorv1alpha1.GroupName,
	Version:  storageMigrationVersion,
	Resource: storageMigrationKind,
}

// CreateStorageMigration creates the migration object via the dynamic client on the cluster.
func Create(client dynamic.Interface, migration *migratorv1alpha1.StorageVersionMigration) (*migratorv1alpha1.StorageVersionMigration, error) {
	// TODO convert existing migration into unstructured
	u := &unstructured.Unstructured{}
	reader := bytes.NewReader([]byte(nil))
	decoder := yaml.NewYAMLOrJSONDecoder(reader, 30)
	if err := decoder.Decode(u); err != nil {
		return nil, fmt.Errorf("converting migration into unstructured: %s", err)
	}

	u, err := client.Resource(migratorGVR).Create(ctx.TODO(), u, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("creating storage migration object: %s", err)
	}

	// TODO convert back
	return nil, nil
}

// CreateObject creates the StorageVersionMigration object from information on the new CRD.
func CreateStorageObject(gvr migratorv1alpha1.GroupVersionResource) (*migratorv1alpha1.StorageVersionMigration, error) {
	m := &migratorv1alpha1.StorageVersionMigration{
		TypeMeta: metav1.TypeMeta{
			Kind:       storageMigrationKind,
			APIVersion: storageMigrationVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceAll,
			GenerateName: "storage-migration-",
		},
		Spec: migratorv1alpha1.StorageVersionMigrationSpec{
			Resource: gvr,
		},
	}

	return m, nil
}


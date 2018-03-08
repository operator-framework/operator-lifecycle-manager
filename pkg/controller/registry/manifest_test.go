package registry

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadCRDFromFile(t *testing.T) {
	testCRDName := "mycoolapp.testing.coreos.com"

	testCRDYaml := []byte(`apiVersion: apiextensions.k8s.io/v1beta1
kind: CustomResourceDefinition
metadata:
  name: mycoolapp.testing.coreos.com
  labels:
    alm-owner-myservice: mycoolapp.clusterserviceversionv1s.app.coreos.com.v1alpha1
  annotations:
    displayName: My Cool App
    description: Mock custom resource definition for testing catalog functionality
spec:
  group: testing.coreos.com
  version: v1alpha1
  scope: Namespaced
  names:
    plural: mycoolapps
    singular: mycoolapp
    kind: MyCoolApp
    listKind: MyCoolAppList
`)

	dir, err := ioutil.TempDir("", "almtest")
	assert.NoError(t, err)

	defer os.RemoveAll(dir) // clean up

	crdfile := filepath.Join(dir, "mycoolapp.crd.yaml")
	assert.NoError(t, ioutil.WriteFile(crdfile, testCRDYaml, 0666))

	catalog := NewInMem()
	loadedCRD, err := LoadCRDFromFile(catalog, crdfile)
	assert.NoError(t, err)
	assert.Equal(t, testCRDName, loadedCRD.GetName())

	crd, err := catalog.FindCRDByKey(CRDKey{
		Name:    testCRDName,
		Kind:    "MyCoolApp",
		Version: "v1alpha1",
	})

	assert.NoError(t, err)
	assert.Equal(t, testCRDName, crd.GetName())
}

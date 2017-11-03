package catalog

import (
	"fmt"
	"io/ioutil"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/client-go/kubernetes/scheme"

	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
)

// LoadCRDFromFile is a utility function for loading the CRD schemas.
func LoadCRDFromFile(m *InMem, filepath string) (*v1beta1.CustomResourceDefinition, error) {
	data, err := ioutil.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("unable to load CRD from file %s: %v", filepath, err)
	}
	crd := v1beta1.CustomResourceDefinition{}
	if _, _, err = scheme.Codecs.UniversalDecoder().Decode(data, nil, &crd); err != nil {
		return nil, fmt.Errorf("could not decode contents of file %s into CRD: %v", filepath, err)
	}
	if err = m.SetCRDDefinition(crd); err != nil {
		return nil, fmt.Errorf("unable to set CRD found in catalog: %v", err)
	}
	return &crd, nil
}

// LoadCSVFromFile is a utility function for loading CSV definitions
func LoadCSVFromFile(m *InMem, filepath string) (*v1alpha1.ClusterServiceVersion, error) {
	data, err := ioutil.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("unable to load CSV from file %s: %v", filepath, err)
	}
	csv := v1alpha1.ClusterServiceVersion{}
	if _, _, err = scheme.Codecs.UniversalDecoder().Decode(data, nil, &csv); err != nil {
		return nil, fmt.Errorf("could not decode contents of file %s into CSV: %v", filepath, err)
	}
	if err = m.SetCSVDefinition(csv); err != nil {
		return nil, fmt.Errorf("unable to set CSV found in catalog: %v", err)
	}
	return &csv, nil
}

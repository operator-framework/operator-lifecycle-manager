package alm

import (
	"io/ioutil"

	v1beta1extensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/client-go/kubernetes/scheme"
)

func LoadCRDFromFile(filepath string) (*v1beta1extensions.CustomResourceDefinition, error) {
	data, err := ioutil.ReadFile(filepath)
	if err != nil {
		return nil, err
	}
	crd := &v1beta1extensions.CustomResourceDefinition{}
	_, _, err = scheme.Codecs.UniversalDecoder().Decode(data, nil, crd)
	return crd, err
}

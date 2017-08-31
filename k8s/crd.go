package k8sutil

import (
	"log"
	"time"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	// (only required to authenticate against GKE clusters).
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

type crdClient struct {
	client       clientset.Interface
	pollInterval time.Duration
	maxWait      time.Duration
	// TODO add logger here
}

func NewCRDClient() *crdClient {
	return &crdClient{
		client:       MustNewKubeExtClient(),
		pollInterval: 500 * time.Millisecond,
		maxWait:      60 * time.Second,
	}
}

func (c *crdClient) CreateCRD(name string, spec v1beta1.CustomResourceDefinitionSpec) error {
	crd := &v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: spec,
	}
	_, err := c.client.ApiextensionsV1beta1().CustomResourceDefinitions().Create(crd)
	if err != nil {
		return err
	}

	// wait for CR creation
	return wait.Poll(c.pollInterval, c.maxWait, func() (bool, error) {
		opts := metav1.GetOptions{}
		crd, err := c.client.ApiextensionsV1beta1().CustomResourceDefinitions().Get(name, opts)
		if err != nil {
			return false, err
		}
		for _, cond := range crd.Status.Conditions {
			switch cond.Type {
			case apiextensionsv1beta1.Established:
				if cond.Status == apiextensionsv1beta1.ConditionTrue {
					return true, err
				}
			case apiextensionsv1beta1.NamesAccepted:
				if cond.Status == apiextensionsv1beta1.ConditionFalse {
					// TODO: more formal logging
					log.Printf("Name conflict: %v", cond.Reason)
				}
			}
		}
		return false, nil
	})
}

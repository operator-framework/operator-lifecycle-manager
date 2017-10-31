package annotater

import (
	"fmt"

	opClient "github.com/coreos-inc/operator-client/pkg/client"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Annotator struct {
	OpClient opClient.Interface
}

func NewAnnotator(opClient opClient.Interface) *Annotator {
	return &Annotator{
		OpClient: opClient,
	}
}

func (a *Annotator) AnnotateNamespaces(namespaceNames []string, annotations map[string]string) error {
	if annotations == nil {
		return nil
	}

	namespaces, err := a.getNamespaces(namespaceNames)
	if err != nil {
		return err
	}

	for _, n := range namespaces {
		if err := a.annotateNamespace(&n, annotations); err != nil {
			return err
		}
	}

	return nil
}

func (a *Annotator) getNamespaces(namespaceNames []string) (namespaces []v1.Namespace, err error) {
	if len(namespaceNames) == 1 && namespaceNames[0] == v1.NamespaceAll {
		namespaceList, err := a.OpClient.KubernetesInterface().CoreV1().Namespaces().List(metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		namespaces = namespaceList.Items
		return namespaces, nil
	}
	for _, n := range namespaceNames {
		namespace, err := a.OpClient.KubernetesInterface().CoreV1().Namespaces().Get(n, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		namespaces = append(namespaces, *namespace)
	}
	return namespaces, nil
}

func (a *Annotator) annotateNamespace(namespace *v1.Namespace, annotations map[string]string) error {
	if namespace.Annotations == nil {
		namespace.Annotations = map[string]string{}
	}

	for key, value := range annotations {
		if existing, ok := namespace.Annotations[key]; ok && existing != value {
			return fmt.Errorf("attempted to annotate namespace %s, but already annotated by %s:%s", namespace.Name, key, existing)
		}
		namespace.Annotations[key] = value
	}

	if _, err := a.OpClient.KubernetesInterface().CoreV1().Namespaces().Update(namespace); err != nil {
		return err
	}
	return nil
}

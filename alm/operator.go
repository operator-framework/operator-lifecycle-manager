package alm

import (
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/rest"

	"github.com/coreos-inc/alm/apis/opver/v1alpha1"
	"github.com/coreos-inc/alm/client"
	"github.com/coreos-inc/alm/install"
	"github.com/coreos-inc/alm/queueinformer"
)

type ALMOperator struct {
	*queueinformer.Operator
	RestClient *rest.RESTClient
}

func NewALMOperator(kubeconfig string) (*ALMOperator, error) {
	opVerClient, err := client.NewOperatorVersionClient(kubeconfig)
	if err != nil {
		return nil, err
	}
	operatorVersionWatcher := cache.NewListWatchFromClient(
		opVerClient,
		"operatorversion-v1s",
		metav1.NamespaceAll,
		fields.Everything(),
	)
	operatorVersionInformer := cache.NewSharedIndexInformer(
		operatorVersionWatcher,
		&v1alpha1.OperatorVersion{},
		15*time.Minute,
		cache.Indexers{},
	)

	queueOperator, err := queueinformer.NewOperator(kubeconfig)
	if err != nil {
		return nil, err
	}
	op := &ALMOperator{
		queueOperator,
		opVerClient,
	}

	opVerQueueInformer := queueinformer.New("operatorversions", operatorVersionInformer, op.syncOperatorVersion, nil)
	op.RegisterQueueInformer(opVerQueueInformer)

	return op, nil
}

func (a *ALMOperator) syncOperatorVersion(obj interface{}) error {
	operatorVersion, ok := obj.(*v1alpha1.OperatorVersion)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting OperatorVersion failed")
	}

	log.Infof("syncing OperatorVersion: %s", operatorVersion.SelfLink)

	resolver := install.NewStrategyResolver(a.OpClient, operatorVersion.ObjectMeta)
	if len(operatorVersion.Spec.Requirements) != 0 && !requirementsMet(operatorVersion.Spec.Requirements, a.RestClient) {
		return fmt.Errorf("requirements were not met: %v", operatorVersion.Spec.Requirements)
	}
	err := resolver.ApplyStrategy(&operatorVersion.Spec.InstallStrategy)
	if err != nil {
		return err
	}

	log.Infof("%s install strategy successful for %s", operatorVersion.Spec.InstallStrategy.StrategyName, operatorVersion.SelfLink)
	return nil
}

func requirementsMet(requirements []v1alpha1.Requirements, kubeClient *rest.RESTClient) bool {
	for _, element := range requirements {
		if element.Optional {
			continue
		}
		result := kubeClient.Get().Namespace(element.Namespace).Name(element.Name).Resource(element.Kind).Do()
		if result.Error() != nil {
			return false
		}
		runtimeObj, err := result.Get()
		if err != nil {
			return false
		}
		if runtimeObj.GetObjectKind().GroupVersionKind().Version != element.ApiVersion {
			return false
		}
	}
	return true
}

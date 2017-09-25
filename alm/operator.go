package alm

import (
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/tools/cache"

	"github.com/coreos-inc/alm/apis/opver/v1alpha1"
	"github.com/coreos-inc/alm/client"
	"github.com/coreos-inc/alm/install"
	"github.com/coreos-inc/alm/operator"
)

type ALMOperator struct {
	*operator.Operator
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

	op := &ALMOperator{}

	opVerLoop := operator.NewQueueInformer("operatorversions", operatorVersionInformer, op.syncOperatorVersion, nil)
	op.Operator, err = operator.NewOperator(kubeconfig, opVerLoop)
	if err != nil {
		return nil, err
	}
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
	err := resolver.ApplyStrategy(&operatorVersion.Spec.InstallStrategy)
	if err != nil {
		return err
	}

	log.Infof("%s install strategy successful for %s", operatorVersion.Spec.InstallStrategy.StrategyName, operatorVersion.SelfLink)
	return nil
}

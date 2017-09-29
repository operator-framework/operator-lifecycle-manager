package alm

import (
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/rest"

	"github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/client"
	"github.com/coreos-inc/alm/install"
	"github.com/coreos-inc/alm/queueinformer"
	"errors"
)

var ErrRequirementsNotMet = errors.New("requirements were not met")

type ALMOperator struct {
	*queueinformer.Operator
	restClient *rest.RESTClient
}

func NewALMOperator(kubeconfig string) (*ALMOperator, error) {
	clusterServiceVersionClient, err := client.NewClusterServiceVersionClient(kubeconfig)
	if err != nil {
		return nil, err
	}
	clusterServiceVersionWatcher := cache.NewListWatchFromClient(
		clusterServiceVersionClient,
		"clusterserviceversion-v1s",
		metav1.NamespaceAll,
		fields.Everything(),
	)
	clusterServiceVersionInformer := cache.NewSharedIndexInformer(
		clusterServiceVersionWatcher,
		&v1alpha1.ClusterServiceVersion{},
		15*time.Minute,
		cache.Indexers{},
	)

	queueOperator, err := queueinformer.NewOperator(kubeconfig)
	if err != nil {
		return nil, err
	}
	op := &ALMOperator{
		queueOperator,
		clusterServiceVersionClient,
	}

	clusterServiceVersionQueueInformer := queueinformer.New("clusterserviceversions", clusterServiceVersionInformer, op.syncClusterServiceVersion, nil)
	op.RegisterQueueInformer(clusterServiceVersionQueueInformer)

	return op, nil
}

func (a *ALMOperator) syncClusterServiceVersion(obj interface{}) error {
	clusterServiceVersion, ok := obj.(*v1alpha1.ClusterServiceVersion)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting ClusterServiceVersion failed")
	}

	log.Infof("syncing ClusterServiceVersion: %s", clusterServiceVersion.SelfLink)

	resolver := install.NewStrategyResolver(a.OpClient, clusterServiceVersion.ObjectMeta)
	ok, err := requirementsMet(clusterServiceVersion.Spec.Requirements, a.restClient)
	if err != nil {
		return err
	}
	if !ok {
		log.Info("requirements were not met: %v", clusterServiceVersion.Spec.Requirements)
		return ErrRequirementsNotMet
	}
	err = resolver.ApplyStrategy(&clusterServiceVersion.Spec.InstallStrategy)
	if err != nil {
		return err
	}

	log.Infof("%s install strategy successful for %s", clusterServiceVersion.Spec.InstallStrategy.StrategyName, clusterServiceVersion.SelfLink)
	return nil
}

func requirementsMet(requirements []v1alpha1.Requirements, kubeClient *rest.RESTClient) (bool, error){
	for _, element := range requirements {
		if element.Optional {
			log.Info("Requirement was optional")
			continue
		}
		result := kubeClient.Get().Namespace(element.Namespace).Name(element.Name).Resource(element.Kind).Do()
		if result.Error() != nil {
			log.Info("Namespace, name, or kind was not met")
			return false, nil
		}
		runtimeObj, err := result.Get()
		if err != nil {
			log.Info("Error retrieving runtimeOBj")
			return false, err
		}
		if runtimeObj.GetObjectKind().GroupVersionKind().Version != element.ApiVersion {
			log.Info("GroupVersionKind was not met")
			return false, nil
		}
	}
	log.Info("Successfully met all requirements")
	return true, nil
}

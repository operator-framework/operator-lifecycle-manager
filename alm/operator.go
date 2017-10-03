package alm

import (
	"errors"
	"fmt"

	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	"github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/client"
	"github.com/coreos-inc/alm/config"
	"github.com/coreos-inc/alm/install"
	"github.com/coreos-inc/alm/queueinformer"
)

var ErrRequirementsNotMet = errors.New("requirements were not met")

type ALMOperator struct {
	*queueinformer.Operator
	restClient *rest.RESTClient
}

func NewALMOperator(kubeconfig string, cfg *config.Config) (*ALMOperator, error) {
	csvClient, err := client.NewClusterServiceVersionClient(kubeconfig)
	if err != nil {
		return nil, err
	}

	csvWatchers := []*cache.ListWatch{}
	for _, namespace := range cfg.Namespaces {
		csvWatcher := cache.NewListWatchFromClient(
			csvClient,
			"clusterserviceversion-v1s",
			namespace,
			fields.Everything(),
		)
		csvWatchers = append(csvWatchers, csvWatcher)
	}

	sharedIndexInformers := []cache.SharedIndexInformer{}
	for _, csvWatcher := range csvWatchers {
		csvInformer := cache.NewSharedIndexInformer(
			csvWatcher,
			&v1alpha1.ClusterServiceVersion{},
			cfg.Interval,
			cache.Indexers{},
		)
		sharedIndexInformers = append(sharedIndexInformers, csvInformer)
	}

	queueOperator, err := queueinformer.NewOperator(kubeconfig)
	if err != nil {
		return nil, err
	}

	op := &ALMOperator{
		queueOperator,
		csvClient,
	}
	csvQueueInformers := queueinformer.New(
		"operatorversions",
		sharedIndexInformers,
		op.syncClusterServiceVersion,
		nil,
	)
	for _, opVerQueueInformer := range csvQueueInformers {
		op.RegisterQueueInformer(opVerQueueInformer)
	}

	return op, nil
}

func (a *ALMOperator) syncClusterServiceVersion(obj interface{}) error {
	clusterServiceVersion, ok := obj.(*v1alpha1.ClusterServiceVersion)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting ClusterServiceVersion failed")
	}

	log.Infof("syncing ClusterServiceVersion: %s", clusterServiceVersion.SelfLink)

	resolver := install.NewStrategyResolver(
		a.OpClient,
		clusterServiceVersion.ObjectMeta,
		clusterServiceVersion.TypeMeta,
	)
	ok, err := requirementsMet(clusterServiceVersion.Spec.CustomResourceDefinitions, a.restClient)
	if err != nil {
		return err
	}
	if !ok {
		log.Info("requirements were not met: %v", clusterServiceVersion.Spec.CustomResourceDefinitions)
		return ErrRequirementsNotMet
	}
	err = resolver.ApplyStrategy(&clusterServiceVersion.Spec.InstallStrategy)
	if err != nil {
		return err
	}

	log.Infof(
		"%s install strategy successful for %s",
		clusterServiceVersion.Spec.InstallStrategy.StrategyName,
		clusterServiceVersion.SelfLink,
	)
	return nil
}

func requirementsMet(crds v1alpha1.CustomResourceDefinitions, kubeClient *rest.RESTClient) (bool, error) {
	// Build a set of the required CRDs.
	requiredCRDSet := map[string]struct{}{}
	for _, crd := range crds.Owned {
		requiredCRDSet[crd] = struct{}{}
	}
	for _, crd := range crds.Required {
		requiredCRDSet[crd] = struct{}{}
	}

	for crd := range requiredCRDSet {
		result := kubeClient.Get().Namespace("").Name(crd).Resource("customresourcedefinition").Do()
		if result.Error() != nil {
			log.Info("Namespace, name, or kind was not met")
			return false, nil
		}
	}
	log.Info("Successfully met all requirements")
	return true, nil
}

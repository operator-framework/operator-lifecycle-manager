package alm

import (
	"context"
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
		"clusterserviceversions",
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

	if clusterServiceVersion.Status.Phase == v1alpha1.CSVPhaseNone {
		log.Infof("scheduling ClusterServiceVersion for requirement verification: %s", clusterServiceVersion.SelfLink)
		clusterServiceVersion.Status.Phase = v1alpha1.CSVPhasePending
		result := &v1alpha1.ClusterServiceVersion{}
		err := a.restClient.Put().Context(context.TODO()).
			Namespace(clusterServiceVersion.Namespace).
			Resource("clusterserviceversion-v1s").
			Name(clusterServiceVersion.Name).
			Body(clusterServiceVersion).
			Do().
			Into(result)
		if err != nil {
			return fmt.Errorf("failed to update CR status: %v", err)
		}
		return nil
	}

	if clusterServiceVersion.Status.Phase == v1alpha1.CSVPhasePending {
		ok, err := requirementsMet(clusterServiceVersion.Spec.CustomResourceDefinitions, a.restClient)
		if err != nil {
			return err
		}
		if !ok {
			log.Info("requirements were not met: %v", clusterServiceVersion.Spec.Requirements)
			return ErrRequirementsNotMet
		}

		log.Infof("scheduling ClusterServiceVersion for install: %s", clusterServiceVersion.SelfLink)
		clusterServiceVersion.Status.Phase = v1alpha1.CSVPhaseInstalling
		result := &v1alpha1.ClusterServiceVersion{}
		err = a.restClient.Put().Context(context.TODO()).
			Namespace(clusterServiceVersion.Namespace).
			Resource("clusterserviceversion-v1s").
			Name(clusterServiceVersion.Name).
			Body(clusterServiceVersion).
			Do().
			Into(result)
		if err != nil {
			return fmt.Errorf("failed to update CR status: %v", err)
		}
		return nil
	}
	if clusterServiceVersion.Status.Phase == v1alpha1.CSVPhaseInstalling {
		resolver := install.NewStrategyResolver(
			a.OpClient,
			clusterServiceVersion.ObjectMeta,
			clusterServiceVersion.TypeMeta,
		)
		err := resolver.ApplyStrategy(&clusterServiceVersion.Spec.InstallStrategy)
		if err != nil {
			clusterServiceVersion.Status.Phase = v1alpha1.CSVPhaseFailed
		} else {
			log.Infof(
				"%s install strategy successful for %s",
				clusterServiceVersion.Spec.InstallStrategy.StrategyName,
				clusterServiceVersion.SelfLink,
			)
			clusterServiceVersion.Status.Phase = v1alpha1.CSVPhaseSucceeded
		}
		result := &v1alpha1.ClusterServiceVersion{}
		err = a.restClient.Put().Context(context.TODO()).
			Namespace(clusterServiceVersion.Namespace).
			Resource("clusterserviceversion-v1s").
			Name(clusterServiceVersion.Name).
			Body(clusterServiceVersion).
			Do().
			Into(result)
		if err != nil {
			return fmt.Errorf("failed to update CR status: %v", err)
		}
		return err

	}

	return nil
}

func (a *ALMOperator) requirementsMet(namespace string, requirements []v1alpha1.Requirements) (bool, error) {
	for _, element := range requirements {
		_, err := a.OpClient.GetCustomResourceDefinitionKind(element.Name)
		if err != nil {
			log.Infof("Couldn't find CRD: %s", err)
			return false, nil
		}
	}
	log.Info("Successfully met all requirements")
	return true, nil
}

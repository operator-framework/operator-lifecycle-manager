package install

import (
	"encoding/json"
	"fmt"

	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/pkg/client"
)

type Strategy interface {
	GetStrategyName() string
}

type StrategyInstaller interface {
	Install(strategy Strategy) error
	CheckInstalled(strategy Strategy) (bool, error)
}

type StrategyResolverInterface interface {
	UnmarshalStrategy(s v1alpha1.NamedInstallStrategy) (strategy Strategy, err error)
	InstallerForStrategy(strategyName string, opClient opClient.Interface, ownerMeta metav1.ObjectMeta, previousStrategy Strategy) StrategyInstaller
}

type StrategyResolver struct{}

func (r *StrategyResolver) UnmarshalStrategy(s v1alpha1.NamedInstallStrategy) (strategy Strategy, err error) {
	switch s.StrategyName {
	case InstallStrategyNameDeployment:
		strategy = &StrategyDetailsDeployment{}
		if err := json.Unmarshal(s.StrategySpecRaw, strategy); err != nil {
			return nil, err
		}
		return
	}
	err = fmt.Errorf("unrecognized install strategy")
	return
}

func (r *StrategyResolver) InstallerForStrategy(strategyName string, opClient opClient.Interface, ownerMeta metav1.ObjectMeta, previousStrategy Strategy) StrategyInstaller {
	switch strategyName {
	case InstallStrategyNameDeployment:
		strategyClient := client.NewInstallStrategyDeploymentClient(opClient, ownerMeta.Namespace)
		return NewStrategyDeploymentInstaller(strategyClient, ownerMeta, previousStrategy)
	}

	// Insurance against these functions being called incorrectly (unmarshal strategy will return a valid strategy name)
	return &NullStrategyInstaller{}
}

type NullStrategyInstaller struct{}

var _ StrategyInstaller = &NullStrategyInstaller{}

func (i *NullStrategyInstaller) Install(s Strategy) error {
	return fmt.Errorf("null InstallStrategy used")
}

func (i *NullStrategyInstaller) CheckInstalled(s Strategy) (bool, error) {
	return true, nil
}

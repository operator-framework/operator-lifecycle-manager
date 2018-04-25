//go:generate counterfeiter -o ../../fakes/fake_strategy.go resolver.go Strategy
//go:generate counterfeiter -o ../../fakes/fake_strategy_installer.go resolver.go StrategyInstaller
//go:generate counterfeiter -o ../../fakes/fake_strategy_resolver.go resolver.go StrategyResolverInterface
package install

import (
	"encoding/json"
	"fmt"

	operatorClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"

	"github.com/coreos/alm/pkg/api/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos/alm/pkg/api/client"
	"github.com/coreos/alm/pkg/lib/ownerutil"
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
	InstallerForStrategy(strategyName string, opClient operatorClient.Interface, owner ownerutil.Owner, previousStrategy Strategy) StrategyInstaller
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

func (r *StrategyResolver) InstallerForStrategy(strategyName string, opClient operatorClient.Interface, owner ownerutil.Owner, previousStrategy Strategy) StrategyInstaller {
	switch strategyName {
	case InstallStrategyNameDeployment:
		strategyClient := client.NewInstallStrategyDeploymentClient(opClient, owner.GetNamespace())
		return NewStrategyDeploymentInstaller(strategyClient, owner, previousStrategy)
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

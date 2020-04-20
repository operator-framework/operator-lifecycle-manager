//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../fakes/fake_strategy.go resolver.go Strategy
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../fakes/fake_strategy_installer.go resolver.go StrategyInstaller
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../fakes/fake_strategy_resolver.go resolver.go StrategyResolverInterface
package install

import (
	"fmt"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/wrappers"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
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
	InstallerForStrategy(strategyName string, opClient operatorclient.ClientInterface, opLister operatorlister.OperatorLister, owner ownerutil.Owner, annotations map[string]string, apiServiceDescriptions []v1alpha1.APIServiceDescription, webhookDescriptions []v1alpha1.WebhookDescription, previousStrategy Strategy) StrategyInstaller
}

type StrategyResolver struct {
	OverridesBuilderFunc DeploymentInitializerBuilderFunc
}

func (r *StrategyResolver) UnmarshalStrategy(s v1alpha1.NamedInstallStrategy) (strategy Strategy, err error) {
	switch s.StrategyName {
	case v1alpha1.InstallStrategyNameDeployment:
		return &s.StrategySpec, nil
	}
	err = fmt.Errorf("unrecognized install strategy")
	return
}

func (r *StrategyResolver) InstallerForStrategy(strategyName string, opClient operatorclient.ClientInterface, opLister operatorlister.OperatorLister, owner ownerutil.Owner, annotations map[string]string, apiServiceDescriptions []v1alpha1.APIServiceDescription, webhookDescriptions []v1alpha1.WebhookDescription, previousStrategy Strategy) StrategyInstaller {
	switch strategyName {
	case v1alpha1.InstallStrategyNameDeployment:
		strategyClient := wrappers.NewInstallStrategyDeploymentClient(opClient, opLister, owner.GetNamespace())

		initializers := []DeploymentInitializerFunc{}
		if r.OverridesBuilderFunc != nil {
			initializers = append(initializers, r.OverridesBuilderFunc(owner))
		}

		return NewStrategyDeploymentInstaller(strategyClient, annotations, owner, previousStrategy, initializers, apiServiceDescriptions, webhookDescriptions)
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

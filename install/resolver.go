package install

import (
	"encoding/json"
	"fmt"

	"github.com/coreos-inc/operator-client/pkg/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
)

type Resolver interface {
	CheckInstalled(s v1alpha1.NamedInstallStrategy, owner metav1.ObjectMeta) (bool, error)
	ApplyStrategy(s v1alpha1.NamedInstallStrategy, owner metav1.ObjectMeta) error
	UnmarshalStrategy(s v1alpha1.NamedInstallStrategy) (strategy Strategy, err error)
}

type Strategy interface {
	Install(client client.Interface, owner metav1.ObjectMeta, ownerType metav1.TypeMeta) error
	CheckInstalled(client client.Interface, owner metav1.ObjectMeta) (bool, error)
}

type StrategyResolver struct {
	client    client.Interface
}

var _ Resolver = &StrategyResolver{}

func NewStrategyResolver(client client.Interface) *StrategyResolver {
	return &StrategyResolver{
		client:    client,
}

func (r *StrategyResolver) CheckInstalled(s v1alpha1.NamedInstallStrategy, owner metav1.ObjectMeta, ownerType metav1.TypeMeta) (bool, error) {
	strategy, err := r.UnmarshalStrategy(s)
	if err != nil {
		return false, err
	}
	return strategy.CheckInstalled(r.client, owner)
}

func (r *StrategyResolver) ApplyStrategy(s v1alpha1.NamedInstallStrategy, owner metav1.ObjectMeta, ownerType metav1.TypeMeta) error {
	strategy, err := r.UnmarshalStrategy(s)
	if err != nil {
		return err
	}
	return strategy.Install(r.client, r.owner, r.ownerType)
}

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

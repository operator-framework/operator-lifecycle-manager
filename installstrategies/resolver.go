package installstrategies

import (
	"encoding/json"
	"fmt"

	"github.com/coreos-inc/operator-client/pkg/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/coreos-inc/alm/apis/opver/v1alpha1"
)

type Strategy interface {
	Install(client client.Interface, owner metav1.ObjectMeta) error
}

type StrategyResolver struct {
	client client.Interface
	owner  metav1.ObjectMeta
}

func NewStrategyResolver(client client.Interface, owner metav1.ObjectMeta) *StrategyResolver {
	return &StrategyResolver{
		client: client,
		owner:  owner,
	}
}

func (r *StrategyResolver) ApplyStrategy(s *v1alpha1.NamedInstallStrategy) error {
	strategy, err := r.UnmarshalStrategy(s)
	if err != nil {
		return err
	}
	return strategy.Install(r.client, r.owner)
}

func (r *StrategyResolver) UnmarshalStrategy(s *v1alpha1.NamedInstallStrategy) (strategy Strategy, err error) {
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

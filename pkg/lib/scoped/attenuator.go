package scoped

import (
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/clients"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

// NewClientAttenuator returns a new instance of ClientAttenuator.
func NewClientAttenuator(logger logrus.FieldLogger, config *rest.Config, kubeclient operatorclient.ClientInterface) *ClientAttenuator {
	return &ClientAttenuator{
		logger: logger,
		retriever: &BearerTokenRetriever{
			kubeclient: kubeclient,
			logger:     logger,
		},
	}
}

// ServiceAccountQuerierFunc returns a reference to the service account from
// which scope client(s) can be created.
// This abstraction allows the attenuator to be agnostic of what the source of user
// specified service accounts are. A user can specify service account(s) for an
// operator group, subscription and CSV.
type ServiceAccountQuerierFunc func() (reference *corev1.ObjectReference, err error)

func StaticQuerier(ref *corev1.ObjectReference) ServiceAccountQuerierFunc {
	return func() (*corev1.ObjectReference, error) {
		return ref, nil
	}
}

// ClientAttenuator returns appropriately scoped client(s) to be used for an
// operator that is being installed.
type ClientAttenuator struct {
	retriever *BearerTokenRetriever
	logger    logrus.FieldLogger
}

func (a *ClientAttenuator) AttenuateToServiceAccount(querier ServiceAccountQuerierFunc) (clients.ConfigTransformer, error) {
	ref, err := querier()
	if err != nil {
		return nil, err
	}

	if ref == nil {
		return clients.ConfigTransformerFunc(func(config *rest.Config) *rest.Config {
			return config
		}), nil
	}

	token, err := a.retriever.Retrieve(ref)
	if err != nil {
		return nil, err
	}

	return clients.ConfigTransformerFunc(func(config *rest.Config) *rest.Config {
		out := rest.AnonymousClientConfig(config)
		out.BearerToken = token
		return out
	}), nil
}

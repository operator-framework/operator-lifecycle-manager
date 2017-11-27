package client

import (
	"context"
	"errors"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"

	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"k8s.io/apiserver/pkg/authentication/serviceaccount"
)

type ClusterServiceVersionInterface interface {
	UpdateCSV(csv *v1alpha1.ClusterServiceVersion) (result *v1alpha1.ClusterServiceVersion, err error)
	CreateCSV(csv *v1alpha1.ClusterServiceVersion) (err error)
	ImpersonatedClientForServiceAccount(serviceAccount string, namespace string) (ClusterServiceVersionInterface, error)
}

type ClusterServiceVersionClient struct {
	*rest.RESTClient
	Config *rest.Config
}

var _ ClusterServiceVersionInterface = &ClusterServiceVersionClient{}

// NewClusterServiceVersionClient creates a client that can interact with the ClusterServiceVersion resource in k8s api
func NewClusterServiceVersionClient(kubeconfig string) (client *ClusterServiceVersionClient, err error) {
	var config *rest.Config
	config, err = getConfig(kubeconfig)
	if err != nil {
		return
	}

	scheme := runtime.NewScheme()
	if err = v1alpha1.AddToScheme(scheme); err != nil {
		return
	}

	config.GroupVersion = &v1alpha1.SchemeGroupVersion
	config.APIPath = "/apis"
	config.ContentType = runtime.ContentTypeJSON
	config.NegotiatedSerializer = serializer.DirectCodecFactory{CodecFactory: serializer.NewCodecFactory(scheme)}

	var restClient *rest.RESTClient
	restClient, err = rest.RESTClientFor(config)
	if err != nil {
		return
	}
	return &ClusterServiceVersionClient{RESTClient: restClient, Config: config}, nil
}

func (c *ClusterServiceVersionClient) UpdateCSV(in *v1alpha1.ClusterServiceVersion) (result *v1alpha1.ClusterServiceVersion, err error) {
	result = &v1alpha1.ClusterServiceVersion{}
	err = c.RESTClient.Put().Context(context.TODO()).
		Namespace(in.Namespace).
		Resource("clusterserviceversion-v1s").
		Name(in.Name).
		Body(in).
		Do().
		Into(result)
	if err != nil {
		err = errors.New("failed to update CR status: " + err.Error())
	}
	return
}

func (c *ClusterServiceVersionClient) CreateCSV(csv *v1alpha1.ClusterServiceVersion) error {
	out := &v1alpha1.ClusterServiceVersion{}
	return c.RESTClient.
		Post().
		Context(context.TODO()).
		Namespace(csv.Namespace).
		Resource("clusterserviceversion-v1s").
		Name(csv.Name).
		Body(csv).
		Do().
		Into(out)
}

// ImpersonatedClientForUser creates a client that impersonates a serviceaccount based on the current client
func (c *ClusterServiceVersionClient) ImpersonatedClientForServiceAccount(serviceAccount string, namespace string) (ClusterServiceVersionInterface, error) {
	impersonatedConfig := CopyConfig(c.Config)
	impersonatedConfig.Impersonate = rest.ImpersonationConfig{
		UserName: serviceaccount.MakeUsername(namespace, serviceAccount),
		Groups:   serviceaccount.MakeGroupNames(namespace, serviceAccount),
	}

	var restClient *rest.RESTClient
	restClient, err := rest.RESTClientFor(impersonatedConfig)
	if err != nil {
		return nil, err
	}
	return &ClusterServiceVersionClient{RESTClient: restClient, Config: impersonatedConfig}, nil
}

// CopyConfig makes a copy of a rest.Config
func CopyConfig(config *rest.Config) *rest.Config {
	return &rest.Config{
		Host:          config.Host,
		APIPath:       config.APIPath,
		Prefix:        config.Prefix,
		ContentConfig: config.ContentConfig,
		Username:      config.Username,
		Password:      config.Password,
		BearerToken:   config.BearerToken,
		Impersonate: rest.ImpersonationConfig{
			Groups:   config.Impersonate.Groups,
			Extra:    config.Impersonate.Extra,
			UserName: config.Impersonate.UserName,
		},
		AuthProvider:        config.AuthProvider,
		AuthConfigPersister: config.AuthConfigPersister,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure:   config.TLSClientConfig.Insecure,
			ServerName: config.TLSClientConfig.ServerName,
			CertFile:   config.TLSClientConfig.CertFile,
			KeyFile:    config.TLSClientConfig.KeyFile,
			CAFile:     config.TLSClientConfig.CAFile,
			CertData:   config.TLSClientConfig.CertData,
			KeyData:    config.TLSClientConfig.KeyData,
			CAData:     config.TLSClientConfig.CAData,
		},
		UserAgent:     config.UserAgent,
		Transport:     config.Transport,
		WrapTransport: config.WrapTransport,
		QPS:           config.QPS,
		Burst:         config.Burst,
		RateLimiter:   config.RateLimiter,
		Timeout:       config.Timeout,
	}
}

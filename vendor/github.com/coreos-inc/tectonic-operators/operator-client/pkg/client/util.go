package client

import (
	"k8s.io/client-go/rest"
)

const (
	serviceAccountUsernamePrefix    = "system:serviceaccount:"
	serviceAccountUsernameSeparator = ":"
	serviceAccountGroupPrefix       = "system:serviceaccounts:"
	allServiceAccountsGroup         = "system:serviceaccounts"
)

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

// MakeUsername generates a username from the given namespace and ServiceAccount name.
// The resulting username can be passed to SplitUsername to extract the original namespace and ServiceAccount name.
func MakeUsername(namespace, name string) string {
	return serviceAccountUsernamePrefix + namespace + serviceAccountUsernameSeparator + name
}

// MakeGroupNames generates service account group names for the given namespace
func MakeGroupNames(namespace string) []string {
	return []string{
		allServiceAccountsGroup,
		MakeNamespaceGroupName(namespace),
	}
}

// MakeNamespaceGroupName returns the name of the group all service accounts in the namespace are included in
func MakeNamespaceGroupName(namespace string) string {
	return serviceAccountGroupPrefix + namespace
}

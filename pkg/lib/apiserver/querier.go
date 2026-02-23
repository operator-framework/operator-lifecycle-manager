package apiserver

import (
	"crypto/tls"
	"fmt"
)

// NoopQuerier returns an instance of noopQuerier. It's used for upstream where
// we don't have any apiserver.config.openshift.io/cluster resource.
func NoopQuerier() Querier {
	return &noopQuerier{}
}

// Querier is an interface that wraps the QueryTLSConfig method.
//
// QueryTLSConfig updates the provided TLS configuration with cluster-wide
// TLS security profile settings (MinVersion, CipherSuites, PreferServerCipherSuites).
type Querier interface {
	QueryTLSConfig(config *tls.Config) error
}

type noopQuerier struct {
}

// QueryTLSConfig applies secure default TLS settings to the provided config.
// This is used on non-OpenShift clusters where there is no apiserver.config.openshift.io/cluster resource,
// but we still want to ensure secure TLS configuration.
func (*noopQuerier) QueryTLSConfig(config *tls.Config) error {
	if config == nil {
		return fmt.Errorf("tls.Config cannot be nil")
	}

	// Apply secure defaults for non-OpenShift clusters
	return ApplySecureDefaults(config)
}

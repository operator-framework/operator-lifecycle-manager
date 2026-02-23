package proxy

import (
	corev1 "k8s.io/api/core/v1"
)

// NoopQuerier returns an instance of noopQuerier. It's used for upstream where
// we don't have any cluster proxy configuration.
func NoopQuerier() Querier {
	return &noopQuerier{}
}

// Querier is an interface that wraps the QueryProxyConfig method.
//
// QueryProxyConfig returns the global cluster level proxy env variable(s).
type Querier interface {
	QueryProxyConfig() (proxy []corev1.EnvVar, err error)
}

type noopQuerier struct {
}

// QueryProxyConfig returns no env variable(s), err is set to nil to indicate
// that the cluster has no global proxy configuration.
func (*noopQuerier) QueryProxyConfig() (proxy []corev1.EnvVar, err error) {
	return
}

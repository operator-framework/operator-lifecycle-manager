package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/filemonitor"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/profile"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

// Option applies a configuration option to the given config.
type Option func(s *serverConfig)

func GetListenAndServeFunc(options ...Option) (func() error, error) {
	sc := defaultServerConfig()
	sc.apply(options)

	return sc.getListenAndServeFunc()
}

func WithTLS(tlsCertPath, tlsKeyPath, clientCAPath *string) Option {
	return func(sc *serverConfig) {
		sc.tlsCertPath = tlsCertPath
		sc.tlsKeyPath = tlsKeyPath
		sc.clientCAPath = clientCAPath
	}
}

func WithLogger(logger *logrus.Logger) Option {
	return func(sc *serverConfig) {
		sc.logger = logger
	}
}

func WithDebug(debug bool) Option {
	return func(sc *serverConfig) {
		sc.debug = debug
	}
}

type serverConfig struct {
	logger       *logrus.Logger
	tlsCertPath  *string
	tlsKeyPath   *string
	clientCAPath *string
	debug        bool
}

func (sc *serverConfig) apply(options []Option) {
	for _, o := range options {
		o(sc)
	}
}

func defaultServerConfig() serverConfig {
	return serverConfig{
		tlsCertPath:  nil,
		tlsKeyPath:   nil,
		clientCAPath: nil,
		logger:       nil,
		debug:        false,
	}
}
func (sc *serverConfig) tlsEnabled() (bool, error) {
	if *sc.tlsCertPath != "" && *sc.tlsKeyPath != "" {
		return true, nil
	}
	if *sc.tlsCertPath != "" || *sc.tlsKeyPath != "" {
		return false, fmt.Errorf("both --tls-key and --tls-crt must be provided for TLS to be enabled")
	}
	return false, nil
}

func (sc *serverConfig) getAddress(tlsEnabled bool) string {
	if tlsEnabled {
		return "127.0.0.1:8443"
	}
	return "127.0.0.1:8080"
}

func (sc serverConfig) getListenAndServeFunc() (func() error, error) {
	tlsEnabled, err := sc.tlsEnabled()
	if err != nil {
		return nil, fmt.Errorf("both --tls-key and --tls-crt must be provided for TLS to be enabled")
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	profile.RegisterHandlers(mux, profile.WithTLS(tlsEnabled || !sc.debug))

	s := http.Server{
		Handler: mux,
		Addr:    sc.getAddress(tlsEnabled),
	}

	if !tlsEnabled {
		return s.ListenAndServe, nil
	}

	sc.logger.Info("TLS keys set, using https for metrics")
	certStore, err := filemonitor.NewCertStore(*sc.tlsCertPath, *sc.tlsKeyPath)
	if err != nil {
		return nil, fmt.Errorf("certificate monitoring for metrics (https) failed: %v", err)
	}

	csw, err := filemonitor.NewWatch(sc.logger, []string{filepath.Dir(*sc.tlsCertPath), filepath.Dir(*sc.tlsKeyPath)}, certStore.HandleFilesystemUpdate)
	if err != nil {
		return nil, fmt.Errorf("error creating cert file watcher: %v", err)
	}
	csw.Run(context.Background())
	certPoolStore, err := filemonitor.NewCertPoolStore(*sc.clientCAPath)
	if err != nil {
		return nil, fmt.Errorf("certificate monitoring for client-ca failed: %v", err)
	}
	cpsw, err := filemonitor.NewWatch(sc.logger, []string{filepath.Dir(*sc.clientCAPath)}, certPoolStore.HandleCABundleUpdate)
	if err != nil {
		return nil, fmt.Errorf("error creating cert file watcher: %v", err)
	}
	cpsw.Run(context.Background())

	s.TLSConfig = &tls.Config{
		GetCertificate: func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return certStore.GetCertificate(), nil
		},
		GetConfigForClient: func(_ *tls.ClientHelloInfo) (*tls.Config, error) {
			var certs []tls.Certificate
			if cert := certStore.GetCertificate(); cert != nil {
				certs = append(certs, *cert)
			}
			return &tls.Config{
				Certificates: certs,
				ClientCAs:    certPoolStore.GetCertPool(),
				ClientAuth:   tls.VerifyClientCertIfGiven,
			}, nil
		},
	}
	return func() error {
		return s.ListenAndServeTLS("", "")
	}, nil
}

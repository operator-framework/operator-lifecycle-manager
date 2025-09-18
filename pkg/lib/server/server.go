package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/filemonitor"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/profile"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
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

func WithKubeConfig(config *rest.Config) Option {
	return func(sc *serverConfig) {
		sc.kubeConfig = config
	}
}

type serverConfig struct {
	logger       *logrus.Logger
	tlsCertPath  *string
	tlsKeyPath   *string
	clientCAPath *string
	kubeConfig   *rest.Config
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
		kubeConfig:   nil,
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
		return ":8443"
	}
	return ":8080"
}

func (sc serverConfig) getListenAndServeFunc() (func() error, error) {
	tlsEnabled, err := sc.tlsEnabled()
	if err != nil {
		return nil, fmt.Errorf("both --tls-key and --tls-crt must be provided for TLS to be enabled")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	profile.RegisterHandlers(mux, profile.WithTLS(tlsEnabled || !sc.debug))

	// Set up authenticated metrics endpoint if kubeConfig is provided
	if sc.kubeConfig != nil && tlsEnabled {
		sc.logger.Info("Setting up authenticated metrics endpoint")
		// Create authentication filter using controller-runtime
		filter, err := filters.WithAuthenticationAndAuthorization(sc.kubeConfig, &http.Client{
			Timeout: 30 * time.Second,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create authentication filter: %w", err)
		}
		// Create authenticated metrics handler
		logger := log.FromContext(context.Background())
		authenticatedMetricsHandler, err := filter(logger, promhttp.Handler())
		if err != nil {
			return nil, fmt.Errorf("failed to wrap metrics handler with authentication: %w", err)
		}
		// Add request logging for debugging if debug mode is enabled
		if sc.debug {
			debugAuthHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sc.logger.Infof("Metrics request from %s, Auth header present: %v, User-Agent: %s",
					r.RemoteAddr, r.Header.Get("Authorization") != "", r.Header.Get("User-Agent"))
				authenticatedMetricsHandler.ServeHTTP(w, r)
			})
			mux.Handle("/metrics", debugAuthHandler)
		} else {
			mux.Handle("/metrics", authenticatedMetricsHandler)
		}
		sc.logger.Info("Metrics endpoint configured with authentication and authorization")
	} else {
		// Fallback to unprotected metrics (for development/testing)
		mux.Handle("/metrics", promhttp.Handler())
		if sc.kubeConfig == nil {
			sc.logger.Warn("No Kubernetes config provided - metrics endpoint will be unprotected")
		} else if !tlsEnabled {
			sc.logger.Warn("TLS not enabled - metrics endpoint will be unprotected")
		}
	}

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
		NextProtos: []string{"http/1.1"}, // Disable HTTP/2 for security
	}
	return func() error {
		return s.ListenAndServeTLS("", "")
	}, nil
}

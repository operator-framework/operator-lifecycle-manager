package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/apiserver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/filemonitor"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/profile"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
)

// certPoolGetter is an interface for getting a certificate pool
type certPoolGetter interface {
	GetCertPool() *x509.CertPool
}

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

func WithAPIServerTLSQuerier(querier apiserver.Querier) Option {
	return func(sc *serverConfig) {
		sc.apiServerTLSQuerier = querier
	}
}

type serverConfig struct {
	logger              *logrus.Logger
	tlsCertPath         *string
	tlsKeyPath          *string
	clientCAPath        *string
	kubeConfig          *rest.Config
	apiServerTLSQuerier apiserver.Querier
	debug               bool
}

func (sc *serverConfig) apply(options []Option) {
	for _, o := range options {
		o(sc)
	}
}

func defaultServerConfig() serverConfig {
	return serverConfig{
		tlsCertPath:         nil,
		tlsKeyPath:          nil,
		clientCAPath:        nil,
		kubeConfig:          nil,
		logger:              nil,
		apiServerTLSQuerier: nil,
		debug:               false,
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

func (sc *serverConfig) clientCAEnabled() bool {
	return sc.clientCAPath != nil && *sc.clientCAPath != ""
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
		sc.logger.Info("Setting up authenticated metrics endpoint with client cert and bearer token support")

		// Create metrics handler that supports BOTH:
		// 1. Bearer token auth (for standalone OCP where Prometheus uses bearer tokens)
		// 2. Client cert auth (for HCP where Prometheus uses client certificates)
		//
		// HCP ServiceMonitors configure client certificates (tlsConfig.cert/keySecret)
		// while standalone OCP uses bearerTokenFile. The TLS layer (--client-ca)
		// already verifies client certs; we just need to accept them as authenticated.

		metricsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check if client presented a verified certificate (HCP)
			if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
				// Client cert was verified by TLS layer via --client-ca
				// Accept it as authenticated and serve metrics
				if sc.debug {
					sc.logger.Infof("Metrics request authenticated via client certificate from %s, CN: %s",
						r.RemoteAddr, r.TLS.PeerCertificates[0].Subject.CommonName)
				}
				promhttp.Handler().ServeHTTP(w, r)
				return
			}

			// No client cert - try bearer token auth (standalone OCP)
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				sc.logger.Warnf("Metrics request rejected: no client cert and no bearer token from %s", r.RemoteAddr)
				http.Error(w, "Unauthorized: client certificate or bearer token required", http.StatusUnauthorized)
				return
			}

			// Validate bearer token via controller-runtime
			httpClient, err := rest.HTTPClientFor(sc.kubeConfig)
			if err != nil {
				sc.logger.WithError(err).Error("Failed to create HTTP client for bearer token auth")
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			filter, err := filters.WithAuthenticationAndAuthorization(sc.kubeConfig, httpClient)
			if err != nil {
				sc.logger.WithError(err).Error("Failed to create auth filter for bearer token")
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			logger := log.FromContext(r.Context())
			authenticatedHandler, err := filter(logger, promhttp.Handler())
			if err != nil {
				sc.logger.WithError(err).Error("Failed to wrap metrics handler")
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			// Let controller-runtime's filter handle bearer token validation
			authenticatedHandler.ServeHTTP(w, r)
		})

		mux.Handle("/metrics", metricsHandler)
		sc.logger.Info("Metrics endpoint configured with dual authentication (client cert + bearer token)")
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

	// Only setup client CA monitoring if clientCAPath is provided
	var certPoolStore certPoolGetter
	if sc.clientCAEnabled() {
		cps, err := filemonitor.NewCertPoolStore(*sc.clientCAPath)
		if err != nil {
			return nil, fmt.Errorf("certificate monitoring for client-ca failed: %v", err)
		}
		cpsw, err := filemonitor.NewWatch(sc.logger, []string{filepath.Dir(*sc.clientCAPath)}, cps.HandleCABundleUpdate)
		if err != nil {
			return nil, fmt.Errorf("error creating cert file watcher: %v", err)
		}
		cpsw.Run(context.Background())
		certPoolStore = cps
	} else {
		sc.logger.Info("No client CA provided, client certificate verification disabled")
	}

	s.TLSConfig = &tls.Config{
		GetCertificate: func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return certStore.GetCertificate(), nil
		},
		GetConfigForClient: func(_ *tls.ClientHelloInfo) (*tls.Config, error) {
			var certs []tls.Certificate
			if cert := certStore.GetCertificate(); cert != nil {
				certs = append(certs, *cert)
			}
			tlsCfg := &tls.Config{
				Certificates: certs,
			}
			// Only configure client CA verification if certPoolStore is available
			if certPoolStore != nil {
				tlsCfg.ClientCAs = certPoolStore.GetCertPool()
				tlsCfg.ClientAuth = tls.VerifyClientCertIfGiven
			}

			// Overlay cluster-wide TLS security profile settings if available
			if sc.apiServerTLSQuerier != nil {
				if err := sc.apiServerTLSQuerier.QueryTLSConfig(tlsCfg); err != nil {
					sc.logger.WithError(err).Warn("Failed to query APIServer TLS config, using defaults")
				}
			}

			return tlsCfg, nil
		},
		NextProtos: []string{"http/1.1"}, // Disable HTTP/2 for security
	}
	return func() error {
		return s.ListenAndServeTLS("", "")
	}, nil
}

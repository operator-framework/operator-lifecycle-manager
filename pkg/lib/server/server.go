package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"time"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/apiserver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/filemonitor"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/profile"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
)

const (
	// metricsReadHeaderTimeout bounds how long a client may take to send request
	// headers (slowloris protection).
	metricsReadHeaderTimeout = 10 * time.Second
	// metricsIdleTimeout bounds how long idle keep-alive connections are retained.
	metricsIdleTimeout = 120 * time.Second
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

// WithClientCAAuthorization configures the metrics endpoint to authorize
// scrapers by verifying their client certificate (mutual TLS) against the
// configured client CA bundle, instead of using the token-based
// authentication/authorization filter (TokenReview + SubjectAccessReview).
//
// This is required in environments where the scraper authenticates with a
// client certificate rather than a bearer token, and where the operator's
// kubeConfig does not point at the same API server that can authenticate the
// scraper's identity (so TokenReview/SubjectAccessReview cannot be used to
// authorize the scraper).
//
// When enabled, both TLS (--tls-cert/--tls-key) and a client CA bundle
// (--client-ca) are required.
func WithClientCAAuthorization(enabled bool) Option {
	return func(sc *serverConfig) {
		sc.clientCAAuthorization = enabled
	}
}

// WithClientCAAllowedCommonNames restricts client-certificate authorization to
// scrapers whose certificate common name is in the given set. It only takes
// effect together with WithClientCAAuthorization. When empty (the default), any
// client certificate that verifies against the configured client CA bundle is
// authorized - the CA bundle itself is the allowlist. When non-empty, a verified
// certificate whose common name is not in the set is rejected with 403.
func WithClientCAAllowedCommonNames(commonNames []string) Option {
	return func(sc *serverConfig) {
		sc.clientCAAllowedCommonNames = commonNames
	}
}

func WithAPIServerTLSQuerier(querier apiserver.Querier) Option {
	return func(sc *serverConfig) {
		sc.apiServerTLSQuerier = querier
	}
}

type serverConfig struct {
	logger                     *logrus.Logger
	tlsCertPath                *string
	tlsKeyPath                 *string
	clientCAPath               *string
	kubeConfig                 *rest.Config
	apiServerTLSQuerier        apiserver.Querier
	debug                      bool
	clientCAAuthorization      bool
	clientCAAllowedCommonNames []string
}

func (sc *serverConfig) apply(options []Option) {
	for _, o := range options {
		o(sc)
	}
}

func defaultServerConfig() serverConfig {
	return serverConfig{
		tlsCertPath:                nil,
		tlsKeyPath:                 nil,
		clientCAPath:               nil,
		kubeConfig:                 nil,
		logger:                     nil,
		apiServerTLSQuerier:        nil,
		debug:                      false,
		clientCAAuthorization:      false,
		clientCAAllowedCommonNames: nil,
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

	// Build the client CA pool early (when TLS and a client CA bundle are
	// configured) so the metrics handler can re-verify client certificates
	// against the current bundle on every request, and so the same hot-reloaded
	// pool backs the TLS handshake below.
	var certPoolStore certPoolGetter
	if tlsEnabled && sc.clientCAEnabled() {
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
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	profile.RegisterHandlers(mux, profile.WithTLS(tlsEnabled || !sc.debug))

	// Set up the metrics endpoint. There are three mutually-exclusive modes:
	//
	//  1. client-certificate authorization (mutual TLS): scrapers are authorized
	//     by verifying their client certificate against the configured client CA
	//     bundle. Used when the scraper authenticates with a client certificate
	//     rather than a bearer token.
	//  2. token-based authentication/authorization: scrapers are authenticated and
	//     authorized via TokenReview/SubjectAccessReview against the kubeConfig's
	//     API server. This is the default on standalone clusters.
	//  3. unprotected: development/testing fallback.
	switch {
	case sc.clientCAAuthorization:
		if !tlsEnabled {
			return nil, fmt.Errorf("--client-ca-authorization requires TLS (--tls-cert and --tls-key)")
		}
		if !sc.clientCAEnabled() {
			return nil, fmt.Errorf("--client-ca-authorization requires a client CA bundle (--client-ca)")
		}
		sc.logger.Info("Setting up metrics endpoint with client-certificate authorization")
		mux.Handle("/metrics", requireVerifiedClientCert(sc.logger, certPoolStore, sc.clientCAAllowedCommonNames, promhttp.Handler()))
		sc.logger.Info("Metrics endpoint configured with client-certificate authorization")
	case sc.kubeConfig != nil && tlsEnabled:
		sc.logger.Info("Setting up authenticated metrics endpoint")
		// Create HTTP client with proper TLS configuration from kubeConfig
		// This is necessary for TokenReview/SubjectAccessReview API calls to verify API server certificates
		httpClient, err := rest.HTTPClientFor(sc.kubeConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create http client for authentication: %w", err)
		}
		// Create authentication filter using controller-runtime
		filter, err := filters.WithAuthenticationAndAuthorization(sc.kubeConfig, httpClient)
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
	default:
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
		// The read/write timeouts are intentionally left unset so that
		// long-running pprof profiles are not truncated.
		ReadHeaderTimeout: metricsReadHeaderTimeout,
		IdleTimeout:       metricsIdleTimeout,
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

	// certPoolStore was built earlier (when a client CA bundle is configured).
	if certPoolStore == nil {
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

// requireVerifiedClientCert wraps the given handler and rejects any request
// whose client certificate does not verify against the server's current client
// CA bundle. The TLS layer is configured with tls.VerifyClientCertIfGiven so
// certificate-less connections still complete (e.g. health probes on other
// endpoints); this handler enforces certificate presence and validity for the
// metrics endpoint.
//
// Verification is performed against clientCAs on every request rather than
// trusting the handshake-time r.TLS.VerifiedChains. This ensures that a rotation
// of the client CA bundle (e.g. a revoked scraper) takes effect immediately,
// even on a reused keep-alive connection whose VerifiedChains was computed
// against the previous bundle.
//
// Requests without a client certificate, or with one that no longer verifies,
// receive 401. When allowedCommonNames is non-empty, a certificate whose common
// name is not in the set is also rejected with 401 (indistinguishable from a
// failed CA verification, so the rejection reason is not leaked to the client).
func requireVerifiedClientCert(logger *logrus.Logger, clientCAs certPoolGetter, allowedCommonNames []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			if logger != nil {
				logger.WithField("remote", r.RemoteAddr).Warn("rejecting metrics request without a client certificate")
			}
			http.Error(w, "client certificate required", http.StatusUnauthorized)
			return
		}
		if err := verifyPeerCertificate(r.TLS.PeerCertificates, clientCAs); err != nil {
			if logger != nil {
				logger.WithField("remote", r.RemoteAddr).WithError(err).Warn("rejecting metrics request with an unverifiable client certificate")
			}
			http.Error(w, "client certificate verification failed", http.StatusUnauthorized)
			return
		}
		if len(allowedCommonNames) > 0 {
			commonName := r.TLS.PeerCertificates[0].Subject.CommonName
			if !slices.Contains(allowedCommonNames, commonName) {
				if logger != nil {
					logger.WithField("commonName", commonName).Warn("rejecting metrics request from client certificate with unauthorized common name")
				}
				// Return the same 401 and message as a failed CA verification so a
				// client cannot distinguish "trusted by the CA but wrong common
				// name" from "not trusted by the CA". The common name is recorded
				// in the server log above for debugging.
				http.Error(w, "client certificate verification failed", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// verifyPeerCertificate verifies the leaf of peerCertificates (the client's
// certificate) against the current client CA pool, using any additionally
// presented certificates as intermediates.
func verifyPeerCertificate(peerCertificates []*x509.Certificate, clientCAs certPoolGetter) error {
	if clientCAs == nil {
		return fmt.Errorf("no client CA bundle configured")
	}
	intermediates := x509.NewCertPool()
	for _, cert := range peerCertificates[1:] {
		intermediates.AddCert(cert)
	}
	_, err := peerCertificates[0].Verify(x509.VerifyOptions{
		Roots:         clientCAs.GetCertPool(),
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	return err
}

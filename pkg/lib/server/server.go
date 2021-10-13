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

func GetListenAndServeFunc(logger *logrus.Logger, tlsCertPath, tlsKeyPath, clientCAPath *string, debug bool) (func() error, error) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	s := http.Server{
		Handler: mux,
		Addr:    ":8080",
	}
	var listenAndServe func() error

	if !debug && *tlsCertPath != "" && *tlsKeyPath != "" {
		logger.Info("TLS keys set, using https for metrics")
		profile.RegisterHandlers(mux)

		certStore, err := filemonitor.NewCertStore(*tlsCertPath, *tlsKeyPath)
		if err != nil {
			return nil, fmt.Errorf("certificate monitoring for metrics (https) failed: %v", err)
		}

		csw, err := filemonitor.NewWatch(logger, []string{filepath.Dir(*tlsCertPath), filepath.Dir(*tlsKeyPath)}, certStore.HandleFilesystemUpdate)
		if err != nil {
			return nil, fmt.Errorf("error creating cert file watcher: %v", err)
		}
		csw.Run(context.Background())
		certPoolStore, err := filemonitor.NewCertPoolStore(*clientCAPath)
		if err != nil {
			return nil, fmt.Errorf("certificate monitoring for client-ca failed: %v", err)
		}
		cpsw, err := filemonitor.NewWatch(logger, []string{filepath.Dir(*clientCAPath)}, certPoolStore.HandleCABundleUpdate)
		if err != nil {
			return nil, fmt.Errorf("error creating cert file watcher: %v", err)
		}
		cpsw.Run(context.Background())

		s.Addr = ":8443"
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

		listenAndServe = func() error {
			return s.ListenAndServeTLS("", "")
		}
	} else if *tlsCertPath != "" || *tlsKeyPath != "" {
		return nil, fmt.Errorf("both --tls-key and --tls-crt must be provided for TLS to be enabled")
	} else {
		options := []profile.Option{}
		if debug {
			logger.Info("TLS keys not set and debug mode enabled, requests to pprof endpoint no longer require certificates")
			options = append(options, profile.DisableTLS)
		}

		profile.RegisterHandlers(mux, options...)
		logger.Info("TLS keys not set, using non-https for metrics and healthz endpoints")
		listenAndServe = s.ListenAndServe
	}

	if listenAndServe == nil {
		return nil, fmt.Errorf("unable to configure healthz/metrics/pprof server")
	}
	return listenAndServe, nil
}

package server

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/golang/glog"
	"github.com/gorilla/mux"
	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/pmorie/osb-broker-lib/pkg/rest"
)

// Server is the server for the OSB REST API and the metrics API. A Server glues
// the HTTP operations to their implementations.
type Server struct {
	// Router is a mux.Router that registers the handlers for the HTTP
	// operations:
	//
	// - OSB API
	// - metrics API
	Router *mux.Router
}

// New creates a new Router and registers all the necessary endpoints and handlers.
func New(api *rest.APISurface, reg prom.Gatherer) *Server {
	router := mux.NewRouter()

	if api.EnableCORS {
		router.Methods("OPTIONS").HandlerFunc(api.OptionsHandler)
	}

	registerAPIHandlers(router, api)
	router.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	return &Server{
		Router: router,
	}
}

// NewHTTPHandler creates a new Router and registers API handlers
func NewHTTPHandler(api *rest.APISurface) http.Handler {
	router := mux.NewRouter()
	registerAPIHandlers(router, api)
	return router
}

// registerAPIHandlers registers the APISurface endpoints and handlers.
func registerAPIHandlers(router *mux.Router, api *rest.APISurface) {
	router.HandleFunc("/v2/catalog", api.GetCatalogHandler).Methods("GET")
	router.HandleFunc("/v2/service_instances/{instance_id}/last_operation", api.LastOperationHandler).Methods("GET")
	router.HandleFunc("/v2/service_instances/{instance_id}", api.ProvisionHandler).Methods("PUT")
	router.HandleFunc("/v2/service_instances/{instance_id}", api.DeprovisionHandler).Methods("DELETE")
	router.HandleFunc("/v2/service_instances/{instance_id}", api.UpdateHandler).Methods("PATCH")
	router.HandleFunc("/v2/service_instances/{instance_id}/service_bindings/{binding_id}", api.BindHandler).Methods("PUT")
	router.HandleFunc("/v2/service_instances/{instance_id}/service_bindings/{binding_id}", api.UnbindHandler).Methods("DELETE")
	router.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})
}

// Run creates the HTTP handler and begins to listen on the specified address.
func (s *Server) Run(ctx context.Context, addr string) error {
	listenAndServe := func(srv *http.Server) error {
		return srv.ListenAndServe()
	}
	return s.run(ctx, addr, listenAndServe)
}

// RunTLS creates the HTTPS handler based on the certifications that were passed
// and begins to listen on the specified address.
func (s *Server) RunTLS(ctx context.Context, addr string, cert string, key string) error {
	var decodedCert, decodedKey []byte
	var tlsCert tls.Certificate
	var err error
	decodedCert, err = base64.StdEncoding.DecodeString(cert)
	if err != nil {
		return err
	}
	decodedKey, err = base64.StdEncoding.DecodeString(key)
	if err != nil {
		return err
	}
	tlsCert, err = tls.X509KeyPair(decodedCert, decodedKey)
	if err != nil {
		return err
	}
	listenAndServe := func(srv *http.Server) error {
		srv.TLSConfig = new(tls.Config)
		srv.TLSConfig.Certificates = []tls.Certificate{tlsCert}
		return srv.ListenAndServeTLS("", "")
	}
	return s.run(ctx, addr, listenAndServe)
}

// RunTLSWithTLSFiles creates the HTTPS handler based on the certification
// files that were passed and begins to listen on the specified address.
func (s *Server) RunTLSWithTLSFiles(ctx context.Context, addr string, certFilePath string, keyFilePath string) error {
	listenAndServe := func(srv *http.Server) error {
		return srv.ListenAndServeTLS(certFilePath, keyFilePath)
	}
	return s.run(ctx, addr, listenAndServe)
}

func (s *Server) run(ctx context.Context, addr string, listenAndServe func(srv *http.Server) error) error {
	glog.Infof("Starting server on %s\n", addr)
	srv := &http.Server{
		Addr:    addr,
		Handler: s.Router,
	}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if srv.Shutdown(c) != nil {
			srv.Close()
		}
	}()
	return listenAndServe(srv)
}

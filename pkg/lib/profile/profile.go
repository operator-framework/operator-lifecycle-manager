package profile

import (
	"net/http"
	"net/http/pprof"
)

type profileConfig struct {
	pprof     bool
	cmdline   bool
	profile   bool
	symbol    bool
	trace     bool
	enableTLS bool
}

// Option applies a configuration option to the given config.
type Option func(p *profileConfig)

func (p *profileConfig) apply(options []Option) {
	for _, o := range options {
		o(p)
	}
}

func WithTLS(enabled bool) Option {
	return func(p *profileConfig) {
		p.enableTLS = enabled
	}
}

func defaultProfileConfig() *profileConfig {
	// Initialize config
	return &profileConfig{
		pprof:     true,
		cmdline:   true,
		profile:   true,
		symbol:    true,
		trace:     true,
		enableTLS: true,
	}
}

// RegisterHandlers registers profile Handlers with the given ServeMux.
//
// The Handlers registered are determined by the given options.
// If no options are given, all available handlers are registered by default.
func RegisterHandlers(mux *http.ServeMux, options ...Option) {
	config := defaultProfileConfig()
	config.apply(options)

	if config.pprof {
		mux.Handle("/debug/pprof/", pprofHandlerFunc(http.HandlerFunc(pprof.Index), config.enableTLS))
	}
	if config.cmdline {
		mux.Handle("/debug/pprof/cmdline", pprofHandlerFunc(http.HandlerFunc(pprof.Cmdline), config.enableTLS))
	}
	if config.profile {
		mux.Handle("/debug/pprof/profile", pprofHandlerFunc(http.HandlerFunc(pprof.Profile), config.enableTLS))
	}
	if config.symbol {
		mux.Handle("/debug/pprof/symbol", pprofHandlerFunc(http.HandlerFunc(pprof.Symbol), config.enableTLS))
	}
	if config.trace {
		mux.Handle("/debug/pprof/trace", pprofHandlerFunc(http.HandlerFunc(pprof.Trace), config.enableTLS))
	}
}

func pprofHandlerFunc(h http.Handler, enableTLS bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if enableTLS && (r.TLS == nil || len(r.TLS.VerifiedChains) == 0) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

package profile

import (
	"net/http"
	"net/http/pprof"
)

type profileConfig struct {
	pprof   bool
	cmdline bool
	profile bool
	symbol  bool
	trace   bool
}

// Option applies a configuration option to the given config.
type Option func(p *profileConfig)

func (p *profileConfig) apply(options []Option) {
	if len(options) == 0 {
		// If no options are given, default to all
		p.pprof = true
		p.cmdline = true
		p.profile = true
		p.symbol = true
		p.trace = true

		return
	}

	for _, o := range options {
		o(p)
	}
}

func defaultProfileConfig() *profileConfig {
	// Initialize config
	return &profileConfig{}
}

// RegisterHandlers registers profile Handlers with the given ServeMux.
//
// The Handlers registered are determined by the given options.
// If no options are given, all available handlers are registered by default.
func RegisterHandlers(mux *http.ServeMux, options ...Option) {
	config := defaultProfileConfig()
	config.apply(options)

	if config.pprof {
		mux.Handle("/debug/pprof/", requireVerifiedClientCertificate(http.HandlerFunc(pprof.Index)))
	}
	if config.cmdline {
		mux.Handle("/debug/pprof/cmdline", requireVerifiedClientCertificate(http.HandlerFunc(pprof.Cmdline)))
	}
	if config.profile {
		mux.Handle("/debug/pprof/profile", requireVerifiedClientCertificate(http.HandlerFunc(pprof.Profile)))
	}
	if config.symbol {
		mux.Handle("/debug/pprof/symbol", requireVerifiedClientCertificate(http.HandlerFunc(pprof.Symbol)))
	}
	if config.trace {
		mux.Handle("/debug/pprof/trace", requireVerifiedClientCertificate(http.HandlerFunc(pprof.Trace)))
	}
}

func requireVerifiedClientCertificate(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

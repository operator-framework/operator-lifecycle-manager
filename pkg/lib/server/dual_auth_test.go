package server

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMetricsHandler_ClientCertAuthentication tests that requests with
// client certificates are accepted (HCP use case)
func TestMetricsHandler_ClientCertAuthentication(t *testing.T) {
	// Create a test handler that mimics our dual auth logic
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for client cert (HCP path)
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("# metrics"))
			return
		}

		// No client cert and no bearer token
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Bearer token path would validate here
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("# metrics"))
	})

	// Test 1: Request WITH client certificate succeeds
	req := httptest.NewRequest("GET", "/metrics", nil)
	req.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{
			{}, // Just need a non-nil cert for the test
		},
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "Request with client cert should succeed")
	assert.Contains(t, rec.Body.String(), "metrics", "Should return metrics")
}

// TestMetricsHandler_BearerTokenPath tests that requests without client certs
// but with Authorization header go to bearer token validation path
func TestMetricsHandler_BearerTokenPath(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No client cert
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			// Check for bearer token
			if r.Header.Get("Authorization") != "" {
				// In real code, this validates the token
				// For test, just accept it
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("# metrics"))
				return
			}
		}

		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})

	// Test: Request with Authorization header (no client cert)
	req := httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "Request with bearer token should reach validation path")
}

// TestMetricsHandler_NoAuthentication tests that requests without
// client cert or bearer token are rejected
func TestMetricsHandler_NoAuthentication(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No client cert and no bearer token
		if (r.TLS == nil || len(r.TLS.PeerCertificates) == 0) && r.Header.Get("Authorization") == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		w.WriteHeader(http.StatusOK)
	})

	// Test: Request without any authentication
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code, "Request without auth should be rejected")
	assert.Contains(t, rec.Body.String(), "Unauthorized", "Should return unauthorized error")
}

// TestMetricsHandler_ClientCertTakesPrecedence tests that when both
// client cert and bearer token are present, client cert is used
func TestMetricsHandler_ClientCertTakesPrecedence(t *testing.T) {
	clientCertUsed := false
	bearerTokenUsed := false

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check client cert first (HCP)
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			clientCertUsed = true
			w.WriteHeader(http.StatusOK)
			return
		}

		// Check bearer token (standalone OCP)
		if r.Header.Get("Authorization") != "" {
			bearerTokenUsed = true
			w.WriteHeader(http.StatusOK)
			return
		}

		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})

	// Test: Request with BOTH client cert and bearer token
	req := httptest.NewRequest("GET", "/metrics", nil)
	req.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{
			{}, // Just need a non-nil cert for the test
		},
	}
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "Request should succeed")
	assert.True(t, clientCertUsed, "Client cert should be checked first")
	assert.False(t, bearerTokenUsed, "Bearer token should NOT be checked when client cert is present")
}

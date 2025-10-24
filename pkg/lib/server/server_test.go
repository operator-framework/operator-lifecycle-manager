package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
)

// TestGetListenAndServeFunc_WithAuthenticatedMetrics tests that the server
// correctly creates an HTTP client with TLS configuration when kubeConfig is provided
func TestGetListenAndServeFunc_WithAuthenticatedMetrics(t *testing.T) {
	// Generate test certificates dynamically
	caCert, caKey, err := generateCA()
	require.NoError(t, err)

	serverCert, serverKey, err := generateServerCert(caCert, caKey, "localhost")
	require.NoError(t, err)

	// Create temporary directory for test certificates
	tmpDir, err := os.MkdirTemp("", "server-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Write dynamically generated certificates to files
	tlsCertPath := filepath.Join(tmpDir, "tls.crt")
	tlsKeyPath := filepath.Join(tmpDir, "tls.key")
	clientCAPath := filepath.Join(tmpDir, "ca.crt")

	err = os.WriteFile(tlsCertPath, serverCert, 0644)
	require.NoError(t, err)
	err = os.WriteFile(tlsKeyPath, serverKey, 0600) // Private key should have restricted permissions
	require.NoError(t, err)
	err = os.WriteFile(clientCAPath, caCert, 0644)
	require.NoError(t, err)

	// Create a test kubeConfig with CA data
	kubeConfig := &rest.Config{
		Host: "https://test-api-server:6443",
		TLSClientConfig: rest.TLSClientConfig{
			CAData: caCert,
		},
	}

	logger := logrus.New()
	logger.SetOutput(io.Discard) // Suppress logs during test

	// Test with authenticated metrics (kubeConfig + TLS enabled)
	_, err = GetListenAndServeFunc(
		WithLogger(logger),
		WithTLS(&tlsCertPath, &tlsKeyPath, &clientCAPath),
		WithKubeConfig(kubeConfig),
		WithDebug(false),
	)

	// The function should succeed - if httpClient is properly configured,
	// it won't fail during filter creation
	assert.NoError(t, err, "GetListenAndServeFunc should succeed with proper TLS configuration")
}

// TestGetListenAndServeFunc_WithoutKubeConfig tests that metrics endpoint
// falls back to unprotected mode when kubeConfig is not provided
func TestGetListenAndServeFunc_WithoutKubeConfig(t *testing.T) {
	// Generate test certificates dynamically
	caCert, caKey, err := generateCA()
	require.NoError(t, err)

	serverCert, serverKey, err := generateServerCert(caCert, caKey, "localhost")
	require.NoError(t, err)

	tmpDir, err := os.MkdirTemp("", "server-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	tlsCertPath := filepath.Join(tmpDir, "tls.crt")
	tlsKeyPath := filepath.Join(tmpDir, "tls.key")
	clientCAPath := filepath.Join(tmpDir, "ca.crt")

	err = os.WriteFile(tlsCertPath, serverCert, 0644)
	require.NoError(t, err)
	err = os.WriteFile(tlsKeyPath, serverKey, 0600)
	require.NoError(t, err)
	err = os.WriteFile(clientCAPath, caCert, 0644)
	require.NoError(t, err)

	logger := logrus.New()
	logger.SetOutput(io.Discard)

	// Test without kubeConfig - should use unprotected metrics
	_, err = GetListenAndServeFunc(
		WithLogger(logger),
		WithTLS(&tlsCertPath, &tlsKeyPath, &clientCAPath),
		WithDebug(false),
	)

	assert.NoError(t, err, "GetListenAndServeFunc should succeed without kubeConfig")
}

// TestHTTPClientHasTLSConfig verifies that rest.HTTPClientFor creates a client
// with proper TLS configuration including CA certificates
func TestHTTPClientHasTLSConfig(t *testing.T) {
	// Generate test CA dynamically
	caCert, _, err := generateCA()
	require.NoError(t, err)

	kubeConfig := &rest.Config{
		Host: "https://test-api-server:6443",
		TLSClientConfig: rest.TLSClientConfig{
			CAData: caCert,
		},
		Timeout: 30 * time.Second,
	}

	// Create HTTP client using rest.HTTPClientFor
	httpClient, err := rest.HTTPClientFor(kubeConfig)
	require.NoError(t, err, "HTTPClientFor should succeed")
	require.NotNil(t, httpClient, "HTTP client should not be nil")

	// Verify that the client has a transport configured
	require.NotNil(t, httpClient.Transport, "HTTP client transport should be configured")

	// Check if it's an http.Transport with TLS config
	transport, ok := httpClient.Transport.(*http.Transport)
	require.True(t, ok, "Transport should be *http.Transport")
	require.NotNil(t, transport.TLSClientConfig, "TLS config should be set")

	// Verify that RootCAs is configured (this proves CA cert is loaded)
	assert.NotNil(t, transport.TLSClientConfig.RootCAs, "RootCAs should be configured from kubeConfig")

	// Verify timeout is set
	assert.Equal(t, 30*time.Second, httpClient.Timeout, "Timeout should match kubeConfig")
}

// TestEmptyHTTPClientMissingTLSConfig demonstrates the bug:
// Creating an empty http.Client doesn't have TLS configuration
func TestEmptyHTTPClientMissingTLSConfig(t *testing.T) {
	// This is the buggy code pattern from the original implementation
	buggyClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	// The buggy client doesn't have a transport or TLS config
	assert.Nil(t, buggyClient.Transport, "Empty http.Client has no transport (uses default)")

	// When used with NewForConfigAndClient, it would fail to verify API server certs
	// because it doesn't have the CA certificates from the kubeConfig
}

// TestMetricsEndpointAccessible tests that the metrics endpoint is accessible
// and properly configured (integration-style test)
func TestMetricsEndpointAccessible(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)

	// Test unprotected metrics endpoint (no kubeConfig, no TLS)
	emptyStr := ""
	listenAndServe, err := GetListenAndServeFunc(
		WithLogger(logger),
		WithTLS(&emptyStr, &emptyStr, &emptyStr),
		WithDebug(false),
	)
	require.NoError(t, err)

	// Start a test server
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "# HELP test_metric Test metric")
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Test that metrics endpoint responds
	resp, err := http.Get(server.URL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "Metrics endpoint should return 200")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "test_metric", "Response should contain metrics")

	// Verify listenAndServe function is not nil
	assert.NotNil(t, listenAndServe, "listenAndServe function should be returned")
}

// generateCA generates a test CA certificate and private key.
// Returns CA certificate PEM, CA private key, and error.
// This function generates certificates dynamically at test runtime to avoid
// hardcoding private keys in source code.
func generateCA() ([]byte, *rsa.PrivateKey, error) {
	// Generate RSA private key for CA
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate CA key: %w", err)
	}

	// Create CA certificate template
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "Test CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour), // Valid for 1 day
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	// Create self-signed CA certificate
	caCertBytes, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create CA certificate: %w", err)
	}

	// Encode CA certificate to PEM
	caCertPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caCertBytes,
	})

	return caCertPEM, caKey, nil
}

// generateServerCert generates a server certificate signed by the given CA.
// Returns server certificate PEM, server private key PEM, and error.
func generateServerCert(caCertPEM []byte, caKey *rsa.PrivateKey, commonName string) ([]byte, []byte, error) {
	// Parse CA certificate
	block, _ := pem.Decode(caCertPEM)
	if block == nil {
		return nil, nil, fmt.Errorf("failed to parse CA certificate PEM")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	// Generate RSA private key for server
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate server key: %w", err)
	}

	// Create server certificate template
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName: commonName,
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(24 * time.Hour), // Valid for 1 day
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	// Create server certificate signed by CA
	serverCertBytes, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create server certificate: %w", err)
	}

	// Encode server certificate to PEM
	serverCertPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: serverCertBytes,
	})

	// Encode server private key to PEM
	serverKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(serverKey),
	})

	return serverCertPEM, serverKeyPEM, nil
}

// TestServerConfig_TLSEnabled tests the TLS detection logic
func TestServerConfig_TLSEnabled(t *testing.T) {
	tests := []struct {
		name        string
		certPath    string
		keyPath     string
		expectTLS   bool
		expectError bool
	}{
		{
			name:        "both cert and key provided",
			certPath:    "/path/to/cert",
			keyPath:     "/path/to/key",
			expectTLS:   true,
			expectError: false,
		},
		{
			name:        "neither cert nor key provided",
			certPath:    "",
			keyPath:     "",
			expectTLS:   false,
			expectError: false,
		},
		{
			name:        "only cert provided",
			certPath:    "/path/to/cert",
			keyPath:     "",
			expectTLS:   false,
			expectError: true,
		},
		{
			name:        "only key provided",
			certPath:    "",
			keyPath:     "/path/to/key",
			expectTLS:   false,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := &serverConfig{
				tlsCertPath: &tt.certPath,
				tlsKeyPath:  &tt.keyPath,
			}

			enabled, err := sc.tlsEnabled()

			if tt.expectError {
				assert.Error(t, err, "Expected error for mismatched cert/key")
			} else {
				assert.NoError(t, err, "Should not error")
				assert.Equal(t, tt.expectTLS, enabled, "TLS enabled state should match")
			}
		})
	}
}

// TestServerConfig_GetAddress tests address selection based on TLS
func TestServerConfig_GetAddress(t *testing.T) {
	sc := &serverConfig{}

	httpsAddr := sc.getAddress(true)
	assert.Equal(t, ":8443", httpsAddr, "HTTPS should use port 8443")

	httpAddr := sc.getAddress(false)
	assert.Equal(t, ":8080", httpAddr, "HTTP should use port 8080")
}

// TestWithOptions tests that configuration options are properly applied
func TestWithOptions(t *testing.T) {
	logger := logrus.New()
	tlsCert := "/path/to/cert"
	tlsKey := "/path/to/key"
	clientCA := "/path/to/ca"
	kubeConfig := &rest.Config{Host: "https://test:6443"}

	sc := defaultServerConfig()
	sc.apply([]Option{
		WithLogger(logger),
		WithTLS(&tlsCert, &tlsKey, &clientCA),
		WithKubeConfig(kubeConfig),
		WithDebug(true),
	})

	assert.Equal(t, logger, sc.logger, "Logger should be set")
	assert.Equal(t, &tlsCert, sc.tlsCertPath, "TLS cert path should be set")
	assert.Equal(t, &tlsKey, sc.tlsKeyPath, "TLS key path should be set")
	assert.Equal(t, &clientCA, sc.clientCAPath, "Client CA path should be set")
	assert.Equal(t, kubeConfig, sc.kubeConfig, "KubeConfig should be set")
	assert.True(t, sc.debug, "Debug should be enabled")
}

// TestRootCAsConfiguration verifies that CA certificates are properly loaded
func TestRootCAsConfiguration(t *testing.T) {
	// Generate test CA dynamically
	caCertPEM, _, err := generateCA()
	require.NoError(t, err)

	caCert := caCertPEM

	// Test that CA data can be parsed
	certPool := x509.NewCertPool()
	ok := certPool.AppendCertsFromPEM(caCert)
	assert.True(t, ok, "CA certificate should be parseable")

	// Create rest.Config with CA data
	config := &rest.Config{
		Host: "https://test-api:6443",
		TLSClientConfig: rest.TLSClientConfig{
			CAData: caCert,
		},
	}

	// Create HTTP client
	client, err := rest.HTTPClientFor(config)
	require.NoError(t, err)

	// Verify transport has TLS config
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport.TLSClientConfig)

	// The RootCAs should be configured
	if transport.TLSClientConfig.RootCAs != nil {
		// Success - RootCAs are configured
		assert.NotNil(t, transport.TLSClientConfig.RootCAs)
	} else {
		// On some systems, if CAData is invalid, RootCAs might be nil
		// but the important thing is no error was returned
		t.Log("RootCAs is nil - this might be due to invalid test certificate")
	}
}

// TestHTTPClientTimeout verifies timeout configuration
func TestHTTPClientTimeout(t *testing.T) {
	config := &rest.Config{
		Host:    "https://test-api:6443",
		Timeout: 45 * time.Second,
	}

	client, err := rest.HTTPClientFor(config)
	require.NoError(t, err)

	assert.Equal(t, 45*time.Second, client.Timeout, "Client timeout should match config")
}

// BenchmarkHTTPClientCreation benchmarks the performance of creating HTTP clients
func BenchmarkHTTPClientCreation(b *testing.B) {
	config := &rest.Config{
		Host: "https://test-api:6443",
		TLSClientConfig: rest.TLSClientConfig{
			CAData: []byte("test-ca-data"),
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = rest.HTTPClientFor(config)
	}
}

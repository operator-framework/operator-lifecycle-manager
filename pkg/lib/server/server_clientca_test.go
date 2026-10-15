package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
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
)

func newTestLogger() *logrus.Logger {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	return logger
}

// testCertPool is a certPoolGetter backed by a fixed pool.
type testCertPool struct {
	pool *x509.CertPool
}

func (t testCertPool) GetCertPool() *x509.CertPool { return t.pool }

// newCAPool generates a CA and returns its PEM, key, and a certPoolGetter
// trusting it.
func newCAPool(t *testing.T) ([]byte, *rsa.PrivateKey, certPoolGetter) {
	t.Helper()
	caPEM, caKey, err := generateCA()
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(caPEM))
	return caPEM, caKey, testCertPool{pool: pool}
}

// signedClientLeaf generates a client-auth certificate with the given common
// name, signed by the given CA, and returns the parsed leaf certificate.
func signedClientLeaf(t *testing.T, caPEM []byte, caKey *rsa.PrivateKey, commonName string) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(caPEM)
	require.NotNil(t, block)
	caCert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return leaf
}

// reqWithClientCert builds a /metrics request presenting the given client leaf.
func reqWithClientCert(leaf *x509.Certificate) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}
	return req
}

func okHandler(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
}

// TestRequireVerifiedClientCert_RejectsRequestWithoutTLS verifies that a
// non-TLS request (no client certificate) is rejected with 401.
func TestRequireVerifiedClientCert_RejectsRequestWithoutTLS(t *testing.T) {
	_, _, pool := newCAPool(t)
	called := false
	handler := requireVerifiedClientCert(newTestLogger(), pool, nil, okHandler(&called))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.False(t, called)
}

// TestRequireVerifiedClientCert_RejectsRequestWithoutClientCert verifies that a
// TLS request presenting no client certificate is rejected with 401.
func TestRequireVerifiedClientCert_RejectsRequestWithoutClientCert(t *testing.T) {
	_, _, pool := newCAPool(t)
	called := false
	handler := requireVerifiedClientCert(newTestLogger(), pool, nil, okHandler(&called))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.TLS = &tls.ConnectionState{} // handshake, but no client cert presented
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.False(t, called)
}

// TestRequireVerifiedClientCert_AllowsCertVerifiedAgainstPool verifies that a
// client certificate that verifies against the current CA pool is allowed when
// no common-name restriction is configured.
func TestRequireVerifiedClientCert_AllowsCertVerifiedAgainstPool(t *testing.T) {
	caPEM, caKey, pool := newCAPool(t)
	leaf := signedClientLeaf(t, caPEM, caKey, "any-cn")
	called := false
	handler := requireVerifiedClientCert(newTestLogger(), pool, nil, okHandler(&called))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, reqWithClientCert(leaf))

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.True(t, called)
}

// TestRequireVerifiedClientCert_RejectsCertNotInCurrentPool verifies that a
// client certificate that no longer verifies against the current CA pool (e.g.
// after a CA rotation, over a reused keep-alive connection) is rejected with
// 401 - even though it was presented and parsed successfully.
func TestRequireVerifiedClientCert_RejectsCertNotInCurrentPool(t *testing.T) {
	// Leaf signed by one CA...
	caPEM, caKey, _ := newCAPool(t)
	leaf := signedClientLeaf(t, caPEM, caKey, "any-cn")
	// ...but the server's current pool trusts a different CA.
	_, _, otherPool := newCAPool(t)
	called := false
	handler := requireVerifiedClientCert(newTestLogger(), otherPool, nil, okHandler(&called))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, reqWithClientCert(leaf))

	assert.Equal(t, http.StatusUnauthorized, rr.Code, "cert not verifiable against the current pool should be rejected")
	assert.False(t, called)
}

// TestRequireVerifiedClientCert_AllowsMatchingCommonName verifies that a
// verified cert whose CN is in the allowed set is allowed.
func TestRequireVerifiedClientCert_AllowsMatchingCommonName(t *testing.T) {
	caPEM, caKey, pool := newCAPool(t)
	leaf := signedClientLeaf(t, caPEM, caKey, "system:serviceaccount:monitoring:prometheus")
	called := false
	handler := requireVerifiedClientCert(newTestLogger(), pool,
		[]string{"system:serviceaccount:monitoring:prometheus"}, okHandler(&called))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, reqWithClientCert(leaf))

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.True(t, called)
}

// TestRequireVerifiedClientCert_RejectsUnlistedCommonName verifies that a
// verified cert whose CN is not in the allowed set is rejected. It returns the
// same 401 as a failed CA verification so the rejection reason is not leaked.
func TestRequireVerifiedClientCert_RejectsUnlistedCommonName(t *testing.T) {
	caPEM, caKey, pool := newCAPool(t)
	leaf := signedClientLeaf(t, caPEM, caKey, "system:serviceaccount:kube-system:attacker")
	called := false
	handler := requireVerifiedClientCert(newTestLogger(), pool,
		[]string{"system:serviceaccount:monitoring:prometheus"}, okHandler(&called))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, reqWithClientCert(leaf))

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.False(t, called)
}

// TestGetListenAndServeFunc_WithClientCAAuthorization verifies that the metrics
// server can be configured to authorize scrapers via client certificate (mTLS)
// instead of the token-based filter, and that it does not require a kubeConfig.
func TestGetListenAndServeFunc_WithClientCAAuthorization(t *testing.T) {
	tlsCertPath, tlsKeyPath, clientCAPath := writeTestCerts(t)

	logger := newTestLogger()

	_, err := GetListenAndServeFunc(
		WithLogger(logger),
		WithTLS(&tlsCertPath, &tlsKeyPath, &clientCAPath),
		WithClientCAAuthorization(true),
		WithDebug(false),
	)
	assert.NoError(t, err, "GetListenAndServeFunc should succeed with client-ca authorization and no kubeConfig")
}

// TestGetListenAndServeFunc_ClientCAAuthorizationRequiresClientCA verifies that
// enabling client-ca authorization without a client CA bundle fails closed.
func TestGetListenAndServeFunc_ClientCAAuthorizationRequiresClientCA(t *testing.T) {
	tlsCertPath, tlsKeyPath, _ := writeTestCerts(t)
	emptyClientCA := ""

	logger := newTestLogger()

	_, err := GetListenAndServeFunc(
		WithLogger(logger),
		WithTLS(&tlsCertPath, &tlsKeyPath, &emptyClientCA),
		WithClientCAAuthorization(true),
		WithDebug(false),
	)
	require.Error(t, err, "client-ca authorization without --client-ca should fail")
	assert.ErrorContains(t, err, "requires a client CA bundle",
		"error should identify the missing client CA bundle")
}

// TestGetListenAndServeFunc_ClientCAAuthorizationRequiresTLS verifies that
// enabling client-ca authorization without TLS fails closed.
func TestGetListenAndServeFunc_ClientCAAuthorizationRequiresTLS(t *testing.T) {
	_, _, clientCAPath := writeTestCerts(t)
	emptyPath := ""

	logger := newTestLogger()

	_, err := GetListenAndServeFunc(
		WithLogger(logger),
		WithTLS(&emptyPath, &emptyPath, &clientCAPath),
		WithClientCAAuthorization(true),
		WithDebug(false),
	)
	require.Error(t, err, "client-ca authorization without TLS should fail")
	assert.ErrorContains(t, err, "requires TLS",
		"error should identify the missing TLS configuration")
}

// TestWithClientCAAuthorizationOption verifies the option sets the config field.
func TestWithClientCAAuthorizationOption(t *testing.T) {
	sc := defaultServerConfig()
	assert.False(t, sc.clientCAAuthorization, "client-ca authorization should default to false")

	sc.apply([]Option{WithClientCAAuthorization(true)})
	assert.True(t, sc.clientCAAuthorization, "WithClientCAAuthorization(true) should enable the mode")
}

// TestWithClientCAAllowedCommonNamesOption verifies the option sets the field.
func TestWithClientCAAllowedCommonNamesOption(t *testing.T) {
	sc := defaultServerConfig()
	assert.Empty(t, sc.clientCAAllowedCommonNames, "allowed common names should default to empty")

	sc.apply([]Option{WithClientCAAllowedCommonNames([]string{"a", "b"})})
	assert.Equal(t, []string{"a", "b"}, sc.clientCAAllowedCommonNames)
}

// writeTestCerts generates a CA and server cert and writes them to a temp dir,
// returning the tls cert, tls key, and client CA paths.
func writeTestCerts(t *testing.T) (string, string, string) {
	t.Helper()

	caCert, caKey, err := generateCA()
	require.NoError(t, err)

	serverCert, serverKey, err := generateServerCert(caCert, caKey, "localhost")
	require.NoError(t, err)

	tmpDir, err := os.MkdirTemp("", "server-clientca-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	tlsCertPath := filepath.Join(tmpDir, "tls.crt")
	tlsKeyPath := filepath.Join(tmpDir, "tls.key")
	clientCAPath := filepath.Join(tmpDir, "ca.crt")

	require.NoError(t, os.WriteFile(tlsCertPath, serverCert, 0644))
	require.NoError(t, os.WriteFile(tlsKeyPath, serverKey, 0600))
	require.NoError(t, os.WriteFile(clientCAPath, caCert, 0644))

	return tlsCertPath, tlsKeyPath, clientCAPath
}

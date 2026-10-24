package certs

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testOrganization = "olm-cert-tests"
	testHost         = "olm-webhook-service.olm.svc"
)

// mustGenerateCA returns a CA valid for a year, failing the test on error.
func mustGenerateCA(t *testing.T) *KeyPair {
	t.Helper()
	ca, err := GenerateCA(time.Now().Add(365*24*time.Hour), testOrganization)
	require.NoError(t, err)
	return ca
}

func TestGenerateCA(t *testing.T) {
	// x509 encodes validity times at second granularity, so request a whole-second
	// notAfter to compare exactly against the value in the parsed certificate.
	notAfter := time.Now().Add(24 * time.Hour).Truncate(time.Second).UTC()

	ca, err := GenerateCA(notAfter, testOrganization)
	require.NoError(t, err)
	require.NotNil(t, ca.Cert)
	require.NotNil(t, ca.Priv)

	require.True(t, ca.Cert.IsCA, "CA cert should have IsCA set")
	require.True(t, ca.Cert.BasicConstraintsValid)
	require.Equal(t, x509.KeyUsageCertSign, ca.Cert.KeyUsage)
	require.Equal(t, []string{testOrganization}, ca.Cert.Subject.Organization)
	require.Contains(t, ca.Cert.Subject.CommonName, "olm-selfsigned-")
	require.True(t, ca.Cert.NotAfter.Equal(notAfter), "notAfter should be the requested value")
	require.Equal(t, "P-256", ca.Priv.Curve.Params().Name)
	require.True(t, Active(ca.Cert), "freshly generated CA should be active")

	// The CA is self-signed: it should verify against a pool containing itself
	// and be able to check its own signature.
	require.NoError(t, ca.Cert.CheckSignatureFrom(ca.Cert))
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	_, err = ca.Cert.Verify(x509.VerifyOptions{Roots: roots})
	require.NoError(t, err)

	_, err = GenerateCA(time.Now().Add(-time.Hour), testOrganization)
	require.ErrorContains(t, err, "invalid notAfter")
}

func TestCreateSignedServingPair(t *testing.T) {
	ca := mustGenerateCA(t)
	notAfter := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second).UTC()
	hosts := []string{testHost, "olm-webhook-service.olm.svc.cluster.local"}

	pair, err := CreateSignedServingPair(notAfter, testOrganization, ca, hosts)
	require.NoError(t, err)
	require.NotNil(t, pair.Cert)
	require.NotNil(t, pair.Priv)

	require.Equal(t, hosts[0], pair.Cert.Subject.CommonName)
	require.Equal(t, hosts, pair.Cert.DNSNames)
	require.Equal(t, []string{testOrganization}, pair.Cert.Subject.Organization)
	require.Equal(t, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, pair.Cert.ExtKeyUsage)
	require.True(t, pair.Cert.NotAfter.Equal(notAfter), "notAfter should be the requested value")
	require.Equal(t, "P-256", pair.Priv.Curve.Params().Name)
	require.True(t, Active(pair.Cert))

	// The serving cert chains to the given CA and is valid for every requested host.
	require.NoError(t, pair.Cert.CheckSignatureFrom(ca.Cert))
	for _, host := range hosts {
		require.NoError(t, VerifyCert(ca.Cert, pair.Cert, host), "cert should verify for host %s", host)
	}

	_, err = CreateSignedServingPair(time.Now().Add(-time.Hour), testOrganization, ca, hosts)
	require.ErrorContains(t, err, "invalid notAfter")

	for _, emptyHosts := range [][]string{nil, {}} {
		_, err = CreateSignedServingPair(notAfter, testOrganization, ca, emptyHosts)
		require.ErrorContains(t, err, "at least one host must be specified", "hosts: %v", emptyHosts)
	}
}

func TestVerifyCert(t *testing.T) {
	ca := mustGenerateCA(t)
	otherCA := mustGenerateCA(t)

	pair, err := CreateSignedServingPair(time.Now().Add(24*time.Hour), testOrganization, ca, []string{testHost})
	require.NoError(t, err)

	require.NoError(t, VerifyCert(ca.Cert, pair.Cert, testHost))

	err = VerifyCert(ca.Cert, pair.Cert, "not-a-san.example.com")
	require.Error(t, err, "cert should not verify for a host missing from its SANs")

	err = VerifyCert(otherCA.Cert, pair.Cert, testHost)
	require.Error(t, err, "cert should not verify against a CA that did not sign it")
}

func TestActive(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		cert   *x509.Certificate
		active bool
	}{
		{
			name:   "within validity window",
			cert:   &x509.Certificate{NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)},
			active: true,
		},
		{
			name:   "not yet valid",
			cert:   &x509.Certificate{NotBefore: now.Add(time.Hour), NotAfter: now.Add(2 * time.Hour)},
			active: false,
		},
		{
			name:   "expired",
			cert:   &x509.Certificate{NotBefore: now.Add(-2 * time.Hour), NotAfter: now.Add(-time.Hour)},
			active: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.active, Active(tt.cert))
		})
	}
}

func TestToPEM(t *testing.T) {
	ca := mustGenerateCA(t)

	certPEM, privPEM, err := ca.ToPEM()
	require.NoError(t, err)

	certBlock, rest := pem.Decode(certPEM)
	require.NotNil(t, certBlock, "cert PEM should decode")
	require.Empty(t, rest)
	require.Equal(t, "CERTIFICATE", certBlock.Type)

	privBlock, rest := pem.Decode(privPEM)
	require.NotNil(t, privBlock, "private key PEM should decode")
	require.Empty(t, rest)
	require.Equal(t, "EC PRIVATE KEY", privBlock.Type)

	roundTripped, err := PEMToCert(certPEM)
	require.NoError(t, err)
	require.True(t, ca.Cert.Equal(roundTripped))

	// A private key that cannot be PEM-marshaled (unsupported curve) returns an error.
	_, _, err = (&KeyPair{Cert: ca.Cert, Priv: &ecdsa.PrivateKey{}}).ToPEM()
	require.Error(t, err)
}

func TestPEMToCert(t *testing.T) {
	ca := mustGenerateCA(t)
	validPEM, privPEM, err := ca.ToPEM()
	require.NoError(t, err)

	badDER := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not a certificate")})

	tests := []struct {
		name    string
		certPEM []byte
		wantErr bool
	}{
		{name: "valid PEM", certPEM: validPEM},
		{name: "nil input", certPEM: nil, wantErr: true},
		{name: "empty input", certPEM: []byte{}, wantErr: true},
		{name: "not PEM encoded", certPEM: []byte("not pem"), wantErr: true},
		{name: "PEM block with non-certificate bytes", certPEM: badDER, wantErr: true},
		{name: "PEM block of non-certificate type", certPEM: privPEM, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cert, err := PEMToCert(tt.certPEM)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, cert)
		})
	}
}

func TestPEMSHA256(t *testing.T) {
	// Well-known SHA-256 digest of the string "hello".
	require.Equal(t, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", PEMSHA256([]byte("hello")))

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("some cert")})
	hash := PEMSHA256(certPEM)
	require.Len(t, hash, 64, "hash should be hex-encoded SHA-256")
	require.Equal(t, hash, PEMSHA256(certPEM), "hash should be deterministic")
	require.NotEqual(t, hash, PEMSHA256([]byte("some other cert")))
}

func TestCertGeneratorFunc(t *testing.T) {
	ca := mustGenerateCA(t)

	var gen CertGenerator = CertGeneratorFunc(CreateSignedServingPair)
	pair, err := gen.Generate(time.Now().Add(24*time.Hour), testOrganization, ca, []string{testHost})
	require.NoError(t, err)
	require.NoError(t, VerifyCert(ca.Cert, pair.Cert, testHost))

	// Errors from the wrapped function pass through unchanged.
	wantErr := errors.New("generate failed")
	failing := CertGeneratorFunc(func(_ time.Time, _ string, _ *KeyPair, _ []string) (*KeyPair, error) {
		return nil, wantErr
	})
	_, err = failing.Generate(time.Now(), testOrganization, ca, []string{testHost})
	require.ErrorIs(t, err, wantErr)
}

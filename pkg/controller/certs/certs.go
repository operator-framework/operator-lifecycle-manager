package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

// KeyPair stores an x509 certificate and its ECDSA private key
type KeyPair struct {
	Cert *x509.Certificate
	Priv *ecdsa.PrivateKey
}

// ToPEM returns the PEM encoded cert pair
func (kp *KeyPair) ToPEM() (certPEM []byte, privPEM []byte, err error) {
	// PEM encode private key
	privDER, err := x509.MarshalECPrivateKey(kp.Priv)
	if err != nil {
		return
	}
	privBlock := &pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: privDER,
	}
	privPEM = pem.EncodeToMemory(privBlock)

	// PEM encode cert
	certBlock := &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: kp.Cert.Raw,
	}
	certPEM = pem.EncodeToMemory(certBlock)

	return
}

// GenerateCA generates a self-signed CA cert/key pair that expires in expiresIn days
func GenerateCA(expiresIn int) (*KeyPair, time.Time, error) {
	if expiresIn > 730 || expiresIn <= 0 {
		return nil, time.Time{}, fmt.Errorf("invalid cert expiration")
	}

	notBefore := time.Now()
	notAfter := notBefore.AddDate(0, 0, expiresIn)

	caDetails := &x509.Certificate{
		//TODO(Nick): figure out what to use for a SerialNumber
		SerialNumber: big.NewInt(1653),
		Subject: pkix.Name{
			Organization: []string{"Red Hat, Inc."},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, time.Time{}, err
	}

	publicKey := &privateKey.PublicKey
	certRaw, err := x509.CreateCertificate(rand.Reader, caDetails, caDetails, publicKey, privateKey)
	if err != nil {
		return nil, time.Time{}, err
	}

	cert, err := x509.ParseCertificate(certRaw)
	if err != nil {
		return nil, time.Time{}, err
	}

	ca := &KeyPair{
		Cert: cert,
		Priv: privateKey,
	}

	return ca, notAfter, nil
}

// CreateSignedServingPair creates a serving cert/key pair signed by the given ca
func CreateSignedServingPair(expiresIn int, ca *KeyPair, hosts []string) (*KeyPair, error) {
	if expiresIn > 730 || expiresIn <= 0 {
		return nil, fmt.Errorf("invalid cert expiration")
	}

	certDetails := &x509.Certificate{
		//TODO(Nick): figure out what to use for a SerialNumber
		SerialNumber: big.NewInt(1653),
		Subject: pkix.Name{
			Organization: []string{"Red Hat, Inc."},
		},
		NotBefore: time.Now(),
		// Valid for 2 years
		NotAfter:              time.Now().AddDate(0, 0, expiresIn),
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		DNSNames:              hosts,
	}

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	publicKey := &privateKey.PublicKey
	certRaw, err := x509.CreateCertificate(rand.Reader, certDetails, ca.Cert, publicKey, ca.Priv)
	if err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(certRaw)
	if err != nil {
		return nil, err
	}

	servingCert := &KeyPair{
		Cert: cert,
		Priv: privateKey,
	}

	return servingCert, nil
}

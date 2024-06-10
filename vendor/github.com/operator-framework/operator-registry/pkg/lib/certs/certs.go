package certs

import (
	"crypto/x509"
	"fmt"
	"os"
)

// RootCAs gets root CAs from system store and the given file
func RootCAs(CaFile string) (*x509.CertPool, error) {
	rootCAs, err := x509.SystemCertPool()
	if err != nil || rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	if len(CaFile) > 0 {
		certs, err := os.ReadFile(CaFile)
		if err != nil {
			return nil, fmt.Errorf("failed to append %q to RootCAs: %v", certs, err)
		}
		if ok := rootCAs.AppendCertsFromPEM(certs); !ok {
			return nil, fmt.Errorf("unable to add certs specified in %s", CaFile)
		}
	}
	return rootCAs, nil
}

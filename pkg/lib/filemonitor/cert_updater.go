package filemonitor

import (
	"crypto/tls"
	"crypto/x509"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
)

type certStore struct {
	mutex      sync.RWMutex
	cert       *tls.Certificate
	tlsCrtPath string
	tlsKeyPath string
}

// NewCertStore returns a store for storing the certificate data and the ability to retrieve it safely
func NewCertStore(tlsCrt, tlsKey string) (*certStore, error) {
	cert, err := tls.LoadX509KeyPair(tlsCrt, tlsKey)
	if err != nil {
		return nil, err
	}
	return &certStore{
		mutex:      sync.RWMutex{},
		cert:       &cert,
		tlsCrtPath: tlsCrt,
		tlsKeyPath: tlsKey,
	}, nil
}

// HandleFilesystemUpdate is intended to be used as the OnUpdateFn for a watcher
// and expects the certificate files to be in the same directory.
func (k *certStore) HandleFilesystemUpdate(logger logrus.FieldLogger, event fsnotify.Event) {
	switch op := event.Op; op {
	case fsnotify.Create:
		logger.Debugf("got fs event for %v", event.Name)

		if err := k.storeCertificate(k.tlsCrtPath, k.tlsKeyPath); err != nil {
			// this can happen if both certificates aren't updated at the same
			// time, but it's okay as replacement only occurs with a valid key pair
			logger.Debugf("certificates not in sync: %v", err)
		} else {
			info, err := x509.ParseCertificate(k.cert.Certificate[0])
			if err != nil {
				logger.Debugf("certificates refreshed, but parsing returned error: %v", err)
			} else {
				logger.Debugf("certificates refreshed: Subject=%v NotBefore=%v NotAfter=%v", info.Subject, info.NotBefore, info.NotAfter)
			}
		}
	}
}

func (k *certStore) storeCertificate(tlsCrt, tlsKey string) error {
	cert, err := tls.LoadX509KeyPair(tlsCrt, tlsKey)
	if err == nil {
		k.mutex.Lock()
		defer k.mutex.Unlock()
		k.cert = &cert
	}
	return err
}

func (k *certStore) GetCertificate() *tls.Certificate {
	k.mutex.RLock()
	defer k.mutex.RUnlock()
	return k.cert
}

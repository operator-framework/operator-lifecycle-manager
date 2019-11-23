package filemonitor

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
)

type keystore struct {
	mutex      sync.RWMutex
	cert       *tls.Certificate
	tlsCrtPath string
	tlsKeyPath string
}

type getCertFn = func(*tls.ClientHelloInfo) (*tls.Certificate, error)

// NewKeystore returns a store for storing the certificate data and the ability to retrieve it safely
func NewKeystore(tlsCrt, tlsKey string) *keystore {
	cert, err := tls.LoadX509KeyPair(tlsCrt, tlsKey)
	if err != nil {
		panic(err)
	}
	return &keystore{
		mutex:      sync.RWMutex{},
		cert:       &cert,
		tlsCrtPath: tlsCrt,
		tlsKeyPath: tlsKey,
	}
}

// HandleFilesystemUpdate is intended to be used as the OnUpdateFn for a watcher
// and expects the certificate files to be in the same directory.
func (k *keystore) HandleFilesystemUpdate(logger *logrus.Logger, event fsnotify.Event) {
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

func (k *keystore) storeCertificate(tlsCrt, tlsKey string) error {
	cert, err := tls.LoadX509KeyPair(tlsCrt, tlsKey)
	if err == nil {
		k.mutex.Lock()
		defer k.mutex.Unlock()
		k.cert = &cert
	}
	return err
}

func (k *keystore) GetCertificate(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
	k.mutex.RLock()
	defer k.mutex.RUnlock()
	return k.cert, nil
}

// OLMGetCertRotationFn is a convenience function for OLM use only, but serves as an example for monitoring file system events
func OLMGetCertRotationFn(logger *logrus.Logger, tlsCertPath, tlsKeyPath string) (getCertFn, error) {
	if filepath.Dir(tlsCertPath) != filepath.Dir(tlsKeyPath) {
		return nil, fmt.Errorf("certificates expected to be in same directory %v vs %v", tlsCertPath, tlsKeyPath)
	}

	keystore := NewKeystore(tlsCertPath, tlsKeyPath)
	watcher, err := NewWatch(logger, []string{filepath.Dir(tlsCertPath)}, keystore.HandleFilesystemUpdate)
	if err != nil {
		return nil, err
	}
	watcher.Run(context.Background())

	return keystore.GetCertificate, nil
}

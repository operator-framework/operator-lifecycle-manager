package filemonitor

import (
	"crypto/x509"
	"fmt"
	"os"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
)

type certPoolStore struct {
	mutex        sync.RWMutex
	certpool     *x509.CertPool
	clientCAPath string
}

func NewCertPoolStore(clientCAPath string) (*certPoolStore, error) {
	pem, err := os.ReadFile(clientCAPath)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("client CA bundle %q contains no parseable certificates", clientCAPath)
	}

	return &certPoolStore{
		mutex:        sync.RWMutex{},
		certpool:     pool,
		clientCAPath: clientCAPath,
	}, nil
}

func (c *certPoolStore) storeCABundle(clientCAPath string) error {
	pem, err := os.ReadFile(clientCAPath)
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		// Preserve the existing (usable) pool rather than replacing it with an
		// empty one that can verify no client certificates.
		return fmt.Errorf("client CA bundle %q contains no parseable certificates", clientCAPath)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.certpool = pool
	return nil
}

func (c *certPoolStore) HandleCABundleUpdate(logger logrus.FieldLogger, event fsnotify.Event) {
	switch op := event.Op; op {
	case fsnotify.Create:
		logger.Debugf("got fs event for %v", event.Name)

		if err := c.storeCABundle(c.clientCAPath); err != nil {
			logger.Debugf("unable to reload ca bundle: %v", err)
		} else {
			logger.Debugf("successfully reload ca bundle: %v", err)
		}
	}
}

func (c *certPoolStore) GetCertPool() *x509.CertPool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.certpool
}

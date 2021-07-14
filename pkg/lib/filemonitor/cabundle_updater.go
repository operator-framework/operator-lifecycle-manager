package filemonitor

import (
	"crypto/x509"
	"io/ioutil"
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
	pem, err := ioutil.ReadFile(clientCAPath)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)

	return &certPoolStore{
		mutex:        sync.RWMutex{},
		certpool:     pool,
		clientCAPath: clientCAPath,
	}, nil
}

func (c *certPoolStore) storeCABundle(clientCAPath string) error {
	pem, err := ioutil.ReadFile(clientCAPath)
	if err == nil {
		c.mutex.Lock()
		defer c.mutex.Unlock()
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(pem)
		c.certpool = pool
	}
	return err
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
	return c.certpool
}

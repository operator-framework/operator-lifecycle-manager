package apiserver

import (
	"crypto/tls"
	"fmt"
	"sync"
	"time"

	"github.com/openshift/client-go/config/informers/externalversions"

	apiconfigv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	configv1 "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/openshiftconfig"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

const (
	// This is the cluster level global apiserver.config.openshift.io/cluster object name.
	globalAPIServerName = "cluster"

	// default sync interval
	defaultSyncInterval = 30 * time.Minute
)

// NewSyncer returns informer and sync functions to enable watch of the apiserver.config.openshift.io/cluster resource.
func NewSyncer(logger *logrus.Logger, client configv1client.Interface) (apiServerInformer configv1.APIServerInformer, syncer *Syncer, querier Querier, factory externalversions.SharedInformerFactory, err error) {
	factory = externalversions.NewSharedInformerFactoryWithOptions(client, defaultSyncInterval)
	apiServerInformer = factory.Config().V1().APIServers()
	s := &Syncer{
		logger:        logger,
		currentConfig: newTLSConfigHolder(),
	}

	syncer = s
	querier = s
	return
}

// RegisterEventHandlers registers event handlers for apiserver.config.openshift.io/cluster resource changes.
// This is a convenience function to set up Add/Update/Delete handlers that call
// the syncer's SyncAPIServer and HandleAPIServerDelete methods.
func RegisterEventHandlers(informer configv1.APIServerInformer, syncer *Syncer) {
	informer.Informer().AddEventHandler(&cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if err := syncer.SyncAPIServer(obj); err != nil {
				syncer.logger.WithError(err).Error("error syncing APIServer on add")
			}
		},
		UpdateFunc: func(_, newObj interface{}) {
			if err := syncer.SyncAPIServer(newObj); err != nil {
				syncer.logger.WithError(err).Error("error syncing APIServer on update")
			}
		},
		DeleteFunc: func(obj interface{}) {
			syncer.HandleAPIServerDelete(obj)
		},
	})
}

// SetupAPIServerTLSConfig sets up the APIServer TLS configuration for HTTPS servers.
// It checks if OpenShift config API is available and if so, creates the necessary
// syncer and informer infrastructure to watch for cluster-wide TLS configuration changes.
//
// Returns:
//   - querier: A Querier that can be used to get TLS configuration (NoopQuerier if OpenShift API not available)
//   - factory: A SharedInformerFactory that must be started after operators are ready (nil if OpenShift API not available)
//   - error: Any error encountered during setup
func SetupAPIServerTLSConfig(logger *logrus.Logger, config *rest.Config) (Querier, interface{ Start(<-chan struct{}) }, error) {
	// Create Kubernetes client for discovery
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating kubernetes client: %w", err)
	}

	// Check if OpenShift config API is available
	openshiftConfigAPIExists, err := openshiftconfig.IsAPIAvailable(clientset.Discovery())
	if err != nil {
		return nil, nil, fmt.Errorf("error checking for OpenShift config API support: %w", err)
	}

	if !openshiftConfigAPIExists {
		return NoopQuerier(), nil, nil
	}

	logger.Info("OpenShift APIServer API available - setting up watch for APIServer TLS configuration")

	// Create versioned config client
	versionedConfigClient, err := configv1client.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("error configuring openshift config client: %w", err)
	}

	// Create syncer and informer
	apiServerInformer, apiServerSyncer, apiServerQuerier, apiServerFactory, err := NewSyncer(logger, versionedConfigClient)
	if err != nil {
		return nil, nil, fmt.Errorf("error initializing APIServer TLS syncer: %w", err)
	}

	logger.Info("APIServer TLS configuration will be applied to HTTPS servers")

	// Register event handlers for APIServer resource changes
	RegisterEventHandlers(apiServerInformer, apiServerSyncer)

	return apiServerQuerier, apiServerFactory, nil
}

// Syncer deals with watching APIServer type(s) on the cluster and let the caller
// query for cluster scoped APIServer TLS configuration.
type Syncer struct {
	logger        *logrus.Logger
	currentConfig *tlsConfigHolder
}

// tlsConfigHolder holds TLS configuration in a thread-safe manner.
// It always contains a valid configuration with secure defaults.
type tlsConfigHolder struct {
	mu     sync.RWMutex
	config tls.Config
}

// newTLSConfigHolder creates a new holder initialized with secure defaults.
func newTLSConfigHolder() *tlsConfigHolder {
	h := &tlsConfigHolder{}
	// Initialize with secure defaults
	_ = ApplySecureDefaults(&h.config)
	return h
}

// update atomically updates the stored TLS configuration.
func (h *tlsConfigHolder) update(minVersion uint16, cipherSuites []uint16) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.config.MinVersion = minVersion
	// Make a defensive copy of the slice
	h.config.CipherSuites = make([]uint16, len(cipherSuites))
	copy(h.config.CipherSuites, cipherSuites)
	h.config.PreferServerCipherSuites = true
}

// copyTo atomically copies the cached TLS settings to the provided config.
// All reading and copying happens under the read lock, ensuring thread safety.
func (h *tlsConfigHolder) copyTo(config *tls.Config) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Copy all fields while holding the lock
	config.MinVersion = h.config.MinVersion
	config.CipherSuites = make([]uint16, len(h.config.CipherSuites))
	copy(config.CipherSuites, h.config.CipherSuites)
	config.PreferServerCipherSuites = h.config.PreferServerCipherSuites
}

// QueryTLSConfig queries the global cluster level APIServer object and updates
// the provided TLS configuration with the cluster-wide security profile settings.
func (w *Syncer) QueryTLSConfig(config *tls.Config) error {
	if config == nil {
		return fmt.Errorf("tls.Config cannot be nil")
	}

	// Copy the current cached config atomically
	// This always succeeds because currentConfig always has a valid value
	w.currentConfig.copyTo(config)
	return nil
}

// SyncAPIServer is invoked when a cluster scoped APIServer object is added or modified.
func (w *Syncer) SyncAPIServer(object interface{}) error {
	apiserver, ok := object.(*apiconfigv1.APIServer)
	if !ok {
		w.logger.Error("wrong type in APIServer syncer")
		return nil
	}

	// Convert the TLS security profile to get new settings
	minVersion, cipherSuites := GetSecurityProfileConfig(apiserver.Spec.TLSSecurityProfile)

	// Check if configuration changed (before updating)
	changed := w.hasConfigChanged(minVersion, cipherSuites)

	// Update the stored configuration atomically
	w.currentConfig.update(minVersion, cipherSuites)

	// Log if configuration changed
	if changed {
		profileName := getProfileName(apiserver.Spec.TLSSecurityProfile)
		w.logger.Infof("APIServer TLS configuration changed: profile=%s, minVersion=%s, cipherCount=%d",
			profileName,
			tlsVersionToString(minVersion),
			len(cipherSuites))
	}

	return nil
}

// HandleAPIServerDelete is invoked when a cluster scoped APIServer object is deleted.
func (w *Syncer) HandleAPIServerDelete(object interface{}) {
	_, ok := object.(*apiconfigv1.APIServer)
	if !ok {
		w.logger.Error("wrong type in APIServer delete syncer")
		return
	}

	// Reset to secure defaults (Intermediate profile)
	w.currentConfig.update(GetSecurityProfileConfig(nil))

	w.logger.Info("APIServer TLS configuration deleted, reverted to secure defaults")
	return
}

// hasConfigChanged checks if the new TLS settings differ from the current cached settings.
func (w *Syncer) hasConfigChanged(minVersion uint16, cipherSuites []uint16) bool {
	w.currentConfig.mu.RLock()
	defer w.currentConfig.mu.RUnlock()

	if w.currentConfig.config.MinVersion != minVersion {
		return true
	}
	if len(w.currentConfig.config.CipherSuites) != len(cipherSuites) {
		return true
	}
	for i := range cipherSuites {
		if w.currentConfig.config.CipherSuites[i] != cipherSuites[i] {
			return true
		}
	}
	return false
}

// getProfileName returns the TLS security profile name for logging.
func getProfileName(profile *apiconfigv1.TLSSecurityProfile) string {
	if profile == nil {
		return "Intermediate (default)"
	}

	profileType := string(profile.Type)
	if profileType == "" {
		return "Intermediate (default)"
	}

	return profileType
}

// tlsVersionToString converts a TLS version number to a string
func tlsVersionToString(version uint16) string {
	switch version {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return "unknown"
	}
}

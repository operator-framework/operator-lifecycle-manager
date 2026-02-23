package apiserver_test

import (
	"crypto/tls"
	"sync"
	"testing"

	apiconfigv1 "github.com/openshift/api/config/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/apiserver"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSyncer_QueryTLSConfig_NilConfig(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(logrus.StandardLogger().Out)

	syncer := &apiserver.Syncer{}

	err := syncer.QueryTLSConfig(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tls.Config cannot be nil")
}

func TestSyncer_QueryTLSConfig_ReturnsDefaults(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(logrus.StandardLogger().Out)

	// Note: We can't easily create a Syncer directly because it requires
	// a lister and currentConfig that are internal. Instead, we'll test
	// that the Querier interface works as expected with NoopQuerier,
	// which has similar behavior for testing purposes.
	querier := apiserver.NoopQuerier()
	config := &tls.Config{}

	err := querier.QueryTLSConfig(config)
	require.NoError(t, err)

	// Verify defaults are applied
	assert.NotZero(t, config.MinVersion, "MinVersion should be set")
	assert.NotEmpty(t, config.CipherSuites, "CipherSuites should be set")
	assert.True(t, config.PreferServerCipherSuites, "PreferServerCipherSuites should be true")
}

func TestSyncer_SyncAPIServer_IntermediateProfile(t *testing.T) {
	// Create a mock APIServer object with Intermediate profile
	server := &apiconfigv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: apiconfigv1.APIServerSpec{
			TLSSecurityProfile: &apiconfigv1.TLSSecurityProfile{
				Type: apiconfigv1.TLSProfileIntermediateType,
			},
		},
	}

	// Test that GetSecurityProfileConfig returns expected values for Intermediate
	minVersion, cipherSuites := apiserver.GetSecurityProfileConfig(server.Spec.TLSSecurityProfile)

	assert.Equal(t, uint16(tls.VersionTLS12), minVersion, "Intermediate should use TLS 1.2")
	assert.NotEmpty(t, cipherSuites, "Should have cipher suites")
	assert.Greater(t, len(cipherSuites), 5, "Intermediate should have multiple ciphers")
}

func TestSyncer_SyncAPIServer_ModernProfile(t *testing.T) {
	// Create a mock APIServer object with Modern profile
	server := &apiconfigv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: apiconfigv1.APIServerSpec{
			TLSSecurityProfile: &apiconfigv1.TLSSecurityProfile{
				Type: apiconfigv1.TLSProfileModernType,
			},
		},
	}

	// Test that GetSecurityProfileConfig returns expected values for Modern
	minVersion, cipherSuites := apiserver.GetSecurityProfileConfig(server.Spec.TLSSecurityProfile)

	assert.Equal(t, uint16(tls.VersionTLS13), minVersion, "Modern should use TLS 1.3")
	assert.NotEmpty(t, cipherSuites, "Should have cipher suites")
}

func TestSyncer_SyncAPIServer_CustomProfile(t *testing.T) {
	// Create a mock APIServer object with Custom profile
	server := &apiconfigv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: apiconfigv1.APIServerSpec{
			TLSSecurityProfile: &apiconfigv1.TLSSecurityProfile{
				Type: apiconfigv1.TLSProfileCustomType,
				Custom: &apiconfigv1.CustomTLSProfile{
					TLSProfileSpec: apiconfigv1.TLSProfileSpec{
						MinTLSVersion: apiconfigv1.VersionTLS13,
						Ciphers: []string{
							"TLS_AES_128_GCM_SHA256",
							"TLS_AES_256_GCM_SHA384",
						},
					},
				},
			},
		},
	}

	// Test that GetSecurityProfileConfig returns expected values for Custom
	minVersion, cipherSuites := apiserver.GetSecurityProfileConfig(server.Spec.TLSSecurityProfile)

	assert.Equal(t, uint16(tls.VersionTLS13), minVersion, "Custom should respect MinTLSVersion")
	assert.NotEmpty(t, cipherSuites, "Should have cipher suites")
}

// TestConcurrentQueryTLSConfig tests thread safety of QueryTLSConfig.
// This simulates multiple goroutines reading the TLS config concurrently.
func TestConcurrentQueryTLSConfig(t *testing.T) {
	querier := apiserver.NoopQuerier()

	// Run multiple goroutines concurrently
	const numGoroutines = 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Channel to collect errors
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()

			config := &tls.Config{}
			if err := querier.QueryTLSConfig(config); err != nil {
				errors <- err
				return
			}

			// Verify the config was populated correctly
			if config.MinVersion == 0 {
				errors <- assert.AnError
				return
			}
			if len(config.CipherSuites) == 0 {
				errors <- assert.AnError
				return
			}
		}()
	}

	wg.Wait()
	close(errors)

	// Check for any errors
	for err := range errors {
		t.Errorf("Concurrent query failed: %v", err)
	}
}

// TestConfigIsolation verifies that modifications to a returned config
// don't affect cached values or other callers.
func TestConfigIsolation(t *testing.T) {
	querier := apiserver.NoopQuerier()

	// Get first config
	config1 := &tls.Config{}
	err := querier.QueryTLSConfig(config1)
	require.NoError(t, err)

	originalMinVersion := config1.MinVersion
	originalCipherCount := len(config1.CipherSuites)

	// Modify the first config
	config1.MinVersion = tls.VersionTLS10
	config1.CipherSuites = []uint16{tls.TLS_RSA_WITH_RC4_128_SHA}

	// Get second config
	config2 := &tls.Config{}
	err = querier.QueryTLSConfig(config2)
	require.NoError(t, err)

	// Verify the second config has the original values, not the modified ones
	assert.Equal(t, originalMinVersion, config2.MinVersion, "MinVersion should not be affected by modifications to other config")
	assert.Equal(t, originalCipherCount, len(config2.CipherSuites), "CipherSuites should not be affected by modifications to other config")
	assert.NotEqual(t, config1.MinVersion, config2.MinVersion, "Configs should be isolated")
}

// TestApplySecureDefaults_PreservesExistingValues tests that
// ApplySecureDefaults only sets values that are zero/empty.
func TestApplySecureDefaults_PreservesExistingValues(t *testing.T) {
	config := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		CipherSuites: []uint16{tls.TLS_AES_256_GCM_SHA384},
	}

	err := apiserver.ApplySecureDefaults(config)
	require.NoError(t, err)

	// MinVersion and CipherSuites should be preserved
	assert.Equal(t, uint16(tls.VersionTLS13), config.MinVersion, "Should preserve existing MinVersion")
	assert.Len(t, config.CipherSuites, 1, "Should preserve existing CipherSuites")
	assert.Equal(t, uint16(tls.TLS_AES_256_GCM_SHA384), config.CipherSuites[0])

	// PreferServerCipherSuites should still be set
	assert.True(t, config.PreferServerCipherSuites, "Should set PreferServerCipherSuites")
}

// TestGetConfigForClient_CreatesFreshConfig tests that the callback
// returns a properly configured TLS config for each connection.
func TestGetConfigForClient_CreatesFreshConfig(t *testing.T) {
	querier := apiserver.NoopQuerier()
	callback := apiserver.GetConfigForClient(querier)
	require.NotNil(t, callback)

	// Call the callback multiple times
	config1, err1 := callback(nil)
	require.NoError(t, err1)
	require.NotNil(t, config1)

	config2, err2 := callback(nil)
	require.NoError(t, err2)
	require.NotNil(t, config2)

	// Each call should return a different config object
	assert.NotSame(t, config1, config2, "Should return fresh config for each connection")

	// But they should have the same values
	assert.Equal(t, config1.MinVersion, config2.MinVersion)
	assert.Equal(t, config1.CipherSuites, config2.CipherSuites)
	assert.Equal(t, config1.PreferServerCipherSuites, config2.PreferServerCipherSuites)
}

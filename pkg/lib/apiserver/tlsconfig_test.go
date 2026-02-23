package apiserver_test

import (
	"crypto/tls"
	"testing"

	apiconfigv1 "github.com/openshift/api/config/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/apiserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSecurityProfileConfig_NilProfile(t *testing.T) {
	// When profile is nil, should use Intermediate defaults
	minVersion, cipherSuites := apiserver.GetSecurityProfileConfig(nil)

	assert.Equal(t, uint16(tls.VersionTLS12), minVersion, "Intermediate profile should use TLS 1.2")
	assert.NotEmpty(t, cipherSuites, "Should have cipher suites")
}

func TestGetSecurityProfileConfig_IntermediateProfile(t *testing.T) {
	profile := &apiconfigv1.TLSSecurityProfile{
		Type: apiconfigv1.TLSProfileIntermediateType,
	}

	minVersion, cipherSuites := apiserver.GetSecurityProfileConfig(profile)

	assert.Equal(t, uint16(tls.VersionTLS12), minVersion, "Intermediate profile should use TLS 1.2")
	assert.NotEmpty(t, cipherSuites, "Should have cipher suites")
	assert.Greater(t, len(cipherSuites), 5, "Intermediate should have multiple cipher suites")
}

func TestGetSecurityProfileConfig_ModernProfile(t *testing.T) {
	profile := &apiconfigv1.TLSSecurityProfile{
		Type: apiconfigv1.TLSProfileModernType,
	}

	minVersion, cipherSuites := apiserver.GetSecurityProfileConfig(profile)

	assert.Equal(t, uint16(tls.VersionTLS13), minVersion, "Modern profile should use TLS 1.3")
	assert.NotEmpty(t, cipherSuites, "Should have cipher suites")
}

func TestGetSecurityProfileConfig_OldProfile(t *testing.T) {
	profile := &apiconfigv1.TLSSecurityProfile{
		Type: apiconfigv1.TLSProfileOldType,
	}

	minVersion, cipherSuites := apiserver.GetSecurityProfileConfig(profile)

	assert.Equal(t, uint16(tls.VersionTLS10), minVersion, "Old profile should use TLS 1.0")
	assert.NotEmpty(t, cipherSuites, "Should have cipher suites")
	assert.Greater(t, len(cipherSuites), 10, "Old profile should have many cipher suites for compatibility")
}

func TestGetSecurityProfileConfig_CustomProfile(t *testing.T) {
	profile := &apiconfigv1.TLSSecurityProfile{
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
	}

	minVersion, cipherSuites := apiserver.GetSecurityProfileConfig(profile)

	assert.Equal(t, uint16(tls.VersionTLS13), minVersion, "Custom profile should respect MinTLSVersion")
	assert.NotEmpty(t, cipherSuites, "Should have cipher suites")
}

func TestGetSecurityProfileConfig_CustomProfileWithoutSpec(t *testing.T) {
	// Custom type but no actual custom spec should fall back to Intermediate
	profile := &apiconfigv1.TLSSecurityProfile{
		Type:   apiconfigv1.TLSProfileCustomType,
		Custom: nil,
	}

	minVersion, cipherSuites := apiserver.GetSecurityProfileConfig(profile)

	assert.Equal(t, uint16(tls.VersionTLS12), minVersion, "Should fall back to Intermediate")
	assert.NotEmpty(t, cipherSuites, "Should have cipher suites")
}

func TestApplySecureDefaults(t *testing.T) {
	tests := []struct {
		name   string
		config *tls.Config
	}{
		{
			name:   "EmptyConfig",
			config: &tls.Config{},
		},
		{
			name: "ConfigWithMinVersionOnly",
			config: &tls.Config{
				MinVersion: tls.VersionTLS13,
			},
		},
		{
			name: "ConfigWithCiphersOnly",
			config: &tls.Config{
				CipherSuites: []uint16{tls.TLS_AES_256_GCM_SHA384},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := apiserver.ApplySecureDefaults(tt.config)
			require.NoError(t, err)

			// Verify defaults are applied
			if tt.name == "EmptyConfig" {
				assert.NotZero(t, tt.config.MinVersion, "MinVersion should be set")
				assert.NotEmpty(t, tt.config.CipherSuites, "CipherSuites should be set")
			}
			assert.True(t, tt.config.PreferServerCipherSuites, "PreferServerCipherSuites should be true")
		})
	}
}

func TestGetConfigForClient(t *testing.T) {
	// Create a mock querier
	querier := apiserver.NoopQuerier()

	// Get the callback function
	callback := apiserver.GetConfigForClient(querier)
	require.NotNil(t, callback)

	// Call the callback
	config, err := callback(nil)
	require.NoError(t, err)
	require.NotNil(t, config)

	// Verify the config has secure defaults
	assert.NotZero(t, config.MinVersion)
	assert.NotEmpty(t, config.CipherSuites)
	assert.True(t, config.PreferServerCipherSuites)
}

func TestCipherNamesToIDs(t *testing.T) {
	tests := []struct {
		name        string
		cipherNames []string
		expectEmpty bool
	}{
		{
			name: "ValidCiphers",
			cipherNames: []string{
				"TLS_AES_128_GCM_SHA256",
				"TLS_AES_256_GCM_SHA384",
			},
			expectEmpty: false,
		},
		{
			name:        "EmptyCiphers",
			cipherNames: []string{},
			expectEmpty: false, // Should fall back to defaults
		},
		{
			name: "InvalidCiphers",
			cipherNames: []string{
				"INVALID_CIPHER_NAME",
			},
			expectEmpty: false, // Should fall back to defaults
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cipherIDs := apiserver.CipherNamesToIDs(tt.cipherNames)

			if tt.expectEmpty {
				assert.Empty(t, cipherIDs)
			} else {
				assert.NotEmpty(t, cipherIDs, "Should have cipher IDs (either valid or defaults)")
			}

			// Verify all cipher IDs are non-zero
			for _, id := range cipherIDs {
				assert.NotZero(t, id, "Cipher ID should not be zero")
			}
		})
	}
}

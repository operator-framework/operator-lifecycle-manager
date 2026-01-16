package apiserver_test

import (
	"crypto/tls"
	"testing"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/apiserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoopQuerier_QueryTLSConfig(t *testing.T) {
	tests := []struct {
		name        string
		config      *tls.Config
		expectError bool
		errorMsg    string
	}{
		{
			name:        "WithNilConfig",
			config:      nil,
			expectError: true,
			errorMsg:    "tls.Config cannot be nil",
		},
		{
			name:        "WithEmptyConfig",
			config:      &tls.Config{},
			expectError: false,
		},
		{
			name: "WithPartialConfig",
			config: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			querier := apiserver.NoopQuerier()
			err := querier.QueryTLSConfig(tt.config)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
				// Verify secure defaults are applied
				assert.NotZero(t, tt.config.MinVersion, "MinVersion should be set to a default")
				assert.NotEmpty(t, tt.config.CipherSuites, "CipherSuites should be set to defaults")
				assert.True(t, tt.config.PreferServerCipherSuites, "PreferServerCipherSuites should be true")
			}
		})
	}
}

func TestNoopQuerier_AppliesSecureDefaults(t *testing.T) {
	querier := apiserver.NoopQuerier()
	config := &tls.Config{}

	err := querier.QueryTLSConfig(config)
	require.NoError(t, err)

	// Verify secure defaults
	assert.GreaterOrEqual(t, config.MinVersion, uint16(tls.VersionTLS12), "Should use at least TLS 1.2")
	assert.NotEmpty(t, config.CipherSuites, "Should have cipher suites configured")

	// Verify cipher suites are valid
	for _, cipher := range config.CipherSuites {
		assert.NotZero(t, cipher, "Cipher suite should not be zero")
	}
}

func TestNoopQuerier_DoesNotOverwriteNonZeroMinVersion(t *testing.T) {
	querier := apiserver.NoopQuerier()
	config := &tls.Config{
		MinVersion: tls.VersionTLS13,
	}

	err := querier.QueryTLSConfig(config)
	require.NoError(t, err)

	// MinVersion should be preserved if already set
	assert.Equal(t, uint16(tls.VersionTLS13), config.MinVersion, "Should preserve existing MinVersion")
}

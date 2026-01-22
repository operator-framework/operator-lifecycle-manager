package apiserver

import (
	"crypto/tls"

	apiconfigv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/crypto"
)

// GetSecurityProfileConfig extracts the minimum TLS version and cipher suites
// from a TLSSecurityProfile object. Converts OpenSSL cipher names to Go TLS cipher IDs.
// If profile is nil, returns config defined by the Intermediate TLS Profile.
func GetSecurityProfileConfig(profile *apiconfigv1.TLSSecurityProfile) (uint16, []uint16) {
	var profileType apiconfigv1.TLSProfileType
	if profile == nil {
		profileType = apiconfigv1.TLSProfileIntermediateType
	} else {
		profileType = profile.Type
	}

	var profileSpec *apiconfigv1.TLSProfileSpec
	if profileType == apiconfigv1.TLSProfileCustomType {
		if profile.Custom != nil {
			profileSpec = &profile.Custom.TLSProfileSpec
		}
	} else {
		profileSpec = apiconfigv1.TLSProfiles[profileType]
	}

	// nothing found / custom type set but no actual custom spec
	if profileSpec == nil {
		profileSpec = apiconfigv1.TLSProfiles[apiconfigv1.TLSProfileIntermediateType]
	}

	// Convert the TLS version string to the Go constant
	minTLSVersion, err := crypto.TLSVersion(string(profileSpec.MinTLSVersion))
	if err != nil {
		// Fallback to default if conversion fails
		minTLSVersion = crypto.DefaultTLSVersion()
	}

	// Convert OpenSSL cipher names to IANA names, then to Go cipher suite IDs
	ianaCipherNames := crypto.OpenSSLToIANACipherSuites(profileSpec.Ciphers)
	cipherSuites := CipherNamesToIDs(ianaCipherNames)

	return minTLSVersion, cipherSuites
}

// CipherNamesToIDs converts IANA cipher suite names to Go TLS cipher suite IDs
func CipherNamesToIDs(cipherNames []string) []uint16 {
	var cipherIDs []uint16

	for _, name := range cipherNames {
		if id, err := crypto.CipherSuite(name); err == nil {
			cipherIDs = append(cipherIDs, id)
		}
	}

	// If no valid ciphers were found, use defaults
	if len(cipherIDs) == 0 {
		cipherIDs = crypto.DefaultCiphers()
	}

	return cipherIDs
}

// ApplySecureDefaults applies secure default TLS settings to the provided config.
// This ensures a minimum security baseline even when no cluster-wide profile is configured.
func ApplySecureDefaults(config *tls.Config) error {
	if config.MinVersion == 0 {
		config.MinVersion = crypto.DefaultTLSVersion()
	}
	if len(config.CipherSuites) == 0 {
		config.CipherSuites = crypto.DefaultCiphers()
	}
	config.PreferServerCipherSuites = true
	return nil
}

// GetConfigForClient returns a GetConfigForClient callback function that can be used
// with tls.Config to provide per-connection dynamic TLS configuration updates.
// This allows the TLS settings to be updated without restarting the server.
//
// Example usage:
//
//	server := &http.Server{
//	    Addr: ":8443",
//	    TLSConfig: &tls.Config{
//	        GetConfigForClient: apiserver.GetConfigForClient(querier),
//	        // Other settings like Certificates, ClientCAs, etc.
//	    },
//	}
func GetConfigForClient(querier Querier) func(*tls.ClientHelloInfo) (*tls.Config, error) {
	return func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		// Create a new config for this connection
		config := &tls.Config{}

		// Apply cluster-wide TLS profile settings
		if err := querier.QueryTLSConfig(config); err != nil {
			return nil, err
		}

		return config, nil
	}
}

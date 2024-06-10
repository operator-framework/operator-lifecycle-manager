package util

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/pkg/image/containerdregistry"
	"github.com/operator-framework/operator-registry/pkg/lib/log"
)

// GetTLSOptions validates and returns TLS options set by opm flags
func GetTLSOptions(cmd *cobra.Command) (bool, bool, error) {
	skipTLS, err := cmd.Flags().GetBool("skip-tls")
	if err != nil {
		return false, false, err
	}
	skipTLSVerify, err := cmd.Flags().GetBool("skip-tls-verify")
	if err != nil {
		return false, false, err
	}
	useHTTP, err := cmd.Flags().GetBool("use-http")
	if err != nil {
		return false, false, err
	}

	switch {
	case cmd.Flags().Changed("skip-tls") && cmd.Flags().Changed("use-http"):
		return false, false, errors.New("invalid flag combination: cannot use --use-http with --skip-tls")
	case cmd.Flags().Changed("skip-tls") && cmd.Flags().Changed("skip-tls-verify"):
		return false, false, errors.New("invalid flag combination: cannot use --skip-tls-verify with --skip-tls")
	case skipTLSVerify && useHTTP:
		return false, false, errors.New("invalid flag combination: --use-http and --skip-tls-verify cannot both be true")
	default:
		// return use HTTP true if just skipTLS
		// is set for functional parity with existing
		if skipTLS {
			return false, true, nil
		}
		return skipTLSVerify, useHTTP, nil
	}
}

// This works in tandem with opm/index/cmd, which adds the relevant flags as persistent
// as part of the root command (cmd/root/cmd) initialization
func CreateCLIRegistry(cmd *cobra.Command) (*containerdregistry.Registry, error) {
	skipTlsVerify, useHTTP, err := GetTLSOptions(cmd)
	if err != nil {
		return nil, err
	}

	cacheDir, err := os.MkdirTemp("", "opm-registry-")
	if err != nil {
		return nil, err
	}

	reg, err := containerdregistry.NewRegistry(
		containerdregistry.WithCacheDir(cacheDir),
		containerdregistry.SkipTLSVerify(skipTlsVerify),
		containerdregistry.WithPlainHTTP(useHTTP),
		containerdregistry.WithLog(log.Null()),
	)
	if err != nil {
		return nil, err
	}
	return reg, nil
}

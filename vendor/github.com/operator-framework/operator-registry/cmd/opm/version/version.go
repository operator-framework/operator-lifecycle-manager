package version

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	// opmVersion is the constant representing the version of the opm binary
	opmVersion = "unknown"
	// gitCommit is a constant representing the source version that
	// generated this build. It should be set during build via -ldflags.
	gitCommit string
	// buildDate in ISO8601 format, output of $(date -u +'%Y-%m-%dT%H:%M:%SZ')
	buildDate string
)

type Version struct {
	OpmVersion string `json:"opmVersion"`
	GitCommit  string `json:"gitCommit"`
	BuildDate  string `json:"buildDate"`
	GoOs       string `json:"goOs"`
	GoArch     string `json:"goArch"`
}

func getVersion() Version {
	return Version{
		OpmVersion: opmVersion,
		GitCommit:  gitCommit,
		BuildDate:  buildDate,
		GoOs:       runtime.GOOS,
		GoArch:     runtime.GOARCH,
	}
}

func (v Version) Print() {
	fmt.Printf("Version: %#v\n", v)
}

func AddCommand(parent *cobra.Command) {
	cmd := &cobra.Command{
		Use:     "version",
		Short:   "Print the opm version",
		Long:    `Print the opm version`,
		Example: `kubebuilder version`,
		Run:     runVersion,
	}

	parent.AddCommand(cmd)
}

func runVersion(_ *cobra.Command, _ []string) {
	getVersion().Print()
}

package provider

import "strings"

const (
	OsLabelPrefix    = "operatorframework.io/os"
	ArchLabelPrefix  = "operatorframework.io/arch"
	DefaultOsLabel   = "operatorframework.io/os.linux"
	DefaultArchLabel = "operatorframework.io/arch.amd64"
	Supported        = "supported"
)

func setDefaultOsArchLabels(labels map[string]string) {
	needsOsLabel := true
	needsArchLabel := true
	for k := range labels {
		if strings.HasPrefix(k, OsLabelPrefix) {
			needsOsLabel = false
		}
		if strings.HasPrefix(k, ArchLabelPrefix) {
			needsArchLabel = false
		}
	}
	if needsOsLabel {
		labels[DefaultOsLabel] = Supported
	}
	if needsArchLabel {
		labels[DefaultArchLabel] = Supported
	}
}

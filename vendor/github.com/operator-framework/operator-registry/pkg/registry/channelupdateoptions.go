package registry

import (
	"fmt"
	"strings"
)

type Mode int

const (
	ReplacesMode = iota
	SemVerMode
	SkipPatchMode
)

func GetModeFromString(mode string) (Mode, error) {
	switch strings.ToLower(mode) {
	case "replaces":
		return ReplacesMode, nil
	case "semver":
		return SemVerMode, nil
	case "semver-skippatch":
		return SkipPatchMode, nil
	default:
		//nolint:staticcheck // ST1005: error message is intentionally capitalized
		return -1, fmt.Errorf("Invalid channel update mode %s specified", mode)
	}
}

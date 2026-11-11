package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blang/semver/v4"
)

const (
	versionReleaseMaxLength = 20
)

// Release represents a pre-release version identifier using period-delimited segments.
// Each segment follows semver pre-release rules: alphanumerics and hyphens only, no leading zeros in numeric identifiers.
// A nil Release represents "no release" and serializes to an empty string in JSON.
// Use nil (not an empty slice) to represent the absence of a release.
type Release []semver.PRVersion

// String returns the string representation of the release version.
func (r Release) String() string {
	if len(r) == 0 {
		return ""
	}
	pres := make([]string, len(r))
	for i, pre := range r {
		pres[i] = pre.String()
	}
	return strings.Join(pres, ".")
}

// Compare compares two Release instances
// a non-zero release is always "greater than" a zero-length release
// otherwise, compares segment by segment using semver pre-release comparison rules
func (r Release) Compare(other Release) int {
	if len(r) == 0 && len(other) > 0 {
		return -1
	}
	if len(other) == 0 && len(r) > 0 {
		return 1
	}
	a := semver.Version{Pre: r}
	b := semver.Version{Pre: other}
	return a.Compare(b)
}

// MarshalJSON implements json.Marshaler for Release.
// It serializes the Release as a period-delimited string.
func (r Release) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.String())
}

// UnmarshalJSON implements json.Unmarshaler for Release.
// It deserializes a period-delimited string into a Release.
func (r *Release) UnmarshalJSON(data []byte) error {
	var ps *string
	if err := json.Unmarshal(data, &ps); err != nil {
		return err
	}

	if ps == nil {
		*r = nil
		return nil
	}

	rel, err := NewRelease(*ps)
	if err != nil {
		return err
	}

	*r = rel
	return nil
}

func NewRelease(relStr string) (Release, error) {
	// empty input is not an error, but results in a nil release
	if relStr == "" {
		return nil, nil
	}

	// Validate against CRD constraint from operators.coreos.com/v1alpha1 ClusterServiceVersion
	if len(relStr) > versionReleaseMaxLength {
		return nil, fmt.Errorf("invalid release %q: exceeds maximum length of %d characters", relStr, versionReleaseMaxLength)
	}

	var (
		segments = strings.Split(relStr, ".")
		r        = make(Release, 0, len(segments))
		errs     []error
	)
	for i, segment := range segments {
		// semver.NewPRVersion validates:
		// - Pattern: alphanumerics and hyphens only
		// - No leading zeros in numeric identifiers
		prVer, err := semver.NewPRVersion(segment)
		if err != nil {
			errs = append(errs, fmt.Errorf("segment %d: %v", i, err))
			continue
		}
		r = append(r, prVer)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("invalid release %q: %v", relStr, err)
	}
	return r, nil
}

// VersionRelease combines a semver Version with an optional Release identifier.
// JSON serialization format:
//   - Version is serialized using standard semver format
//   - Release is always included as a string field (empty string if nil)
//   - Example: {"version":"1.2.3","release":"alpha.1"}
//   - Example: {"version":"1.2.3","release":""}
type VersionRelease struct {
	Version semver.Version `json:"version"`
	Release Release        `json:"release"`
}

// KubernetesSafeString returns the safe string representation of the version release suitable for use as a metadata.name
// this generation is not round-trippable (i.e. cannot be parsed back into the original VersionRelease)
func (vr *VersionRelease) KubernetesSafeString() string {
	// Replace '+' with '-' and lowercase for DNS1123 compliance
	buildMetadataReplacer := strings.NewReplacer("+", "-")
	var result string
	if len(vr.Release) > 0 {
		result = fmt.Sprintf("%s-%s", buildMetadataReplacer.Replace(vr.Version.String()), vr.Release.String())
	} else {
		result = buildMetadataReplacer.Replace(vr.Version.String())
	}
	return strings.ToLower(result)
}

func (vr *VersionRelease) Compare(other *VersionRelease) int {
	if cmp := vr.Version.Compare(other.Version); cmp != 0 {
		return cmp
	}
	return vr.Release.Compare(other.Release)
}

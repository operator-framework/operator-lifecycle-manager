package semver

import (
	"fmt"

	"github.com/blang/semver"
)

// BuildIdCompare compares two versions and returns negative one if the first arg is less than the second arg, positive one if it is larger, and zero if they are equal.
// This comparison follows typical semver precedence rules, with one addition: whenever two versions are equal with the exception of their build-ids, the build-ids are compared using prerelease precedence rules. Further, versions with no build-id are always less than versions with build-ids; e.g. 1.0.0 < 1.0.0+1.
func BuildIdCompare(b semver.Version, v semver.Version) (int, error) {
	if c := b.Compare(v); c != 0 {
		return c, nil
	}

	bPre, err := buildAsPrerelease(b)
	if err != nil {
		return 0, fmt.Errorf("failed to convert build-id of %s to prerelease version for comparison: %s", b, err)
	}

	vPre, err := buildAsPrerelease(v)
	if err != nil {
		return 0, fmt.Errorf("failed to convert build-id of %s to prerelease version for comparison: %s", v, err)
	}

	return bPre.Compare(*vPre), nil
}

func buildAsPrerelease(v semver.Version) (*semver.Version, error) {
	var pre []semver.PRVersion
	for _, b := range v.Build {
		p, err := semver.NewPRVersion(b)
		if err != nil {
			return nil, err
		}
		pre = append(pre, p)
	}

	var major uint64
	if len(pre) > 0 {
		// Adjust for the case where we compare a build-id prerelease analog to a version without a build-id.
		// Without this `0.0.0+1` and `0.0.0` would become `0.0.0-1` and `0.0.0`, where the rules of prerelease comparison would
		// end up giving us the wrong result; i.e. `0.0.0+1` < `0.0.0`. With this, `0.0.0+1` and `0.0.0` become `1.0.0-1` and `0.0.0`
		// respectively, which does yield the intended result.
		major = 1
	}

	return &semver.Version{
		Major: major,
		Minor: 0,
		Patch: 0,
		Pre:   pre,
	}, nil
}

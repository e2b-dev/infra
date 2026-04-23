package fcversion

import (
	"strings"

	"github.com/Masterminds/semver/v3"
)

type Info struct {
	commitHash         string
	lastReleaseVersion semver.Version
}

func stripVersionPrefix(version string) string {
	return strings.TrimPrefix(version, "v")
}

func New(fcVersion string) (info Info, err error) {
	// The structure of the fcVersion is last_tag[-prerelease]_commit_hash
	// Example: v1.0.0-release_1234567

	parts := strings.Split(fcVersion, "_")

	versionString := stripVersionPrefix(parts[0])

	version, versionErr := semver.NewVersion(versionString)
	if versionErr != nil {
		return info, versionErr
	}

	info.lastReleaseVersion = *version
	if len(parts) > 1 {
		info.commitHash = parts[1]
	}

	return info, nil
}

func (v *Info) Version() semver.Version {
	return v.lastReleaseVersion
}

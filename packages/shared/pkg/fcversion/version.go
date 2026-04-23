// Package fcversion parses firecracker version strings and exposes feature
// flags derived from the parsed semver. The version string format produced by
// our build pipeline is "vX.Y.Z[-prerelease]_<short-commit>"; see
// packages/shared/pkg/featureflags/flags.go for the defaults and resolution
// map.
package fcversion

import (
	"strings"

	"github.com/Masterminds/semver/v3"
)

// Info carries the parsed semver release and the optional commit short-hash
// suffix of a firecracker version string.
type Info struct {
	commitHash         string
	lastReleaseVersion semver.Version
}

// New parses a firecracker version string of the form
// "vX.Y.Z[-prerelease]_<commit>". The "_<commit>" suffix is optional.
func New(fcVersion string) (Info, error) {
	var info Info

	parts := strings.Split(fcVersion, "_")
	versionString := strings.TrimPrefix(parts[0], "v")

	version, err := semver.NewVersion(versionString)
	if err != nil {
		return info, err
	}

	info.lastReleaseVersion = *version
	if len(parts) > 1 {
		info.commitHash = parts[1]
	}

	return info, nil
}

// Version returns the semver portion of the firecracker version.
func (v *Info) Version() semver.Version {
	return v.lastReleaseVersion
}

// HasHugePages reports whether the firecracker binary supports huge pages.
// Huge page support landed in firecracker v1.7. Getting this wrong flips the
// memfile page size between 2 MiB and 4 KiB, which corrupts the memory file
// for the binary that is actually launched — keep the result aligned with the
// firecracker binary the orchestrator selects, not the one the API preferred.
func (v *Info) HasHugePages() bool {
	return v.lastReleaseVersion.Major() > 1 ||
		(v.lastReleaseVersion.Major() == 1 && v.lastReleaseVersion.Minor() >= 7)
}

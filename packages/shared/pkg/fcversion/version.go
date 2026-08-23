package fcversion

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// ErrInvalidVersion reports a version string that is in none of the known
// formats. Callers must not fall back to guessing a format.
var ErrInvalidVersion = errors.New("invalid firecracker version")

// e2bVersionRe matches vX.Y-a.b.c: the upstream release line X.Y plus the
// e2b semver a.b.c, whose major is the snapshot/fork-API compatibility contract.
var e2bVersionRe = regexp.MustCompile(`^v(\d+\.\d+)-(\d+\.\d+\.\d+)$`)

type format int

const (
	formatUnknown format = iota // zero value: not a parsed version
	formatLegacy                // last_tag[-prerelease]_commit_hash, e.g. v1.14.1_431f1fc
	formatBare                  // upstream semver only, e.g. v1.10.1 (dev builds)
	formatE2B                   // vX.Y-<e2b-semver>, e.g. v1.14-0.1.0
)

type Info struct {
	format             format
	commitHash         string
	lastReleaseVersion semver.Version
	e2bVersion         semver.Version
}

func stripVersionPrefix(version string) string {
	return strings.TrimPrefix(version, "v")
}

func New(fcVersion string) (info Info, err error) {
	wrapInvalid := func(parseErr error) error {
		return fmt.Errorf("%w %q: %w", ErrInvalidVersion, fcVersion, parseErr)
	}

	if strings.Contains(fcVersion, "_") {
		parts := strings.Split(fcVersion, "_")

		version, versionErr := semver.StrictNewVersion(stripVersionPrefix(parts[0]))
		if versionErr != nil {
			return info, wrapInvalid(versionErr)
		}

		info.format = formatLegacy
		info.lastReleaseVersion = *version
		info.commitHash = parts[1]

		return info, nil
	}

	if m := e2bVersionRe.FindStringSubmatch(fcVersion); m != nil {
		line, versionErr := semver.StrictNewVersion(m[1] + ".0")
		if versionErr != nil {
			return info, wrapInvalid(versionErr)
		}

		e2bVersion, versionErr := semver.StrictNewVersion(m[2])
		if versionErr != nil {
			return info, wrapInvalid(versionErr)
		}

		info.format = formatE2B
		info.lastReleaseVersion = *line
		info.e2bVersion = *e2bVersion

		return info, nil
	}

	version, versionErr := semver.StrictNewVersion(stripVersionPrefix(fcVersion))
	if versionErr != nil {
		return info, wrapInvalid(versionErr)
	}

	info.format = formatBare
	info.lastReleaseVersion = *version

	return info, nil
}

// Version returns the upstream Firecracker version. For the e2b format only
// the release line is known, so patch is always 0.
func (v *Info) Version() semver.Version {
	return v.lastReleaseVersion
}

// E2BVersion returns the e2b release semver carried by e2b-format versions.
// ok is false for the other formats.
func (v *Info) E2BVersion() (e2bVersion semver.Version, ok bool) {
	if v.format != formatE2B {
		return semver.Version{}, false
	}

	return v.e2bVersion, true
}

// LDKey returns the key this version resolves through in the LaunchDarkly
// firecracker-versions map: vX.Y for legacy versions, vX.Y-<e2b-major> for
// e2b-format versions. ok is false for bare upstream versions, which never
// came from the release pipeline and have no LD line.
func (v *Info) LDKey() (key string, ok bool) {
	switch v.format {
	case formatLegacy:
		return fmt.Sprintf("v%d.%d", v.lastReleaseVersion.Major(), v.lastReleaseVersion.Minor()), true
	case formatE2B:
		return fmt.Sprintf("v%d.%d-%d", v.lastReleaseVersion.Major(), v.lastReleaseVersion.Minor(), v.e2bVersion.Major()), true
	default:
		return "", false
	}
}

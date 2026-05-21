package utils

import (
	"fmt"

	"golang.org/x/mod/semver"
)

const MinEnvdVersionForSnapshot = "0.5.0"

// MinEnvdVersionForCgroupFreeze is the first envd that exposes a native
// POST /freeze endpoint and thaws cgroups on /init. We require both so the
// orchestrator can avoid shell-based freezes (slow under load) and so the
// sandbox doesn't end up permanently frozen after resume on older envds.
const MinEnvdVersionForCgroupFreeze = "0.6.0"

func sanitizeVersion(version string) string {
	if len(version) > 0 && version[0] != 'v' {
		version = "v" + version
	}

	return version
}

func CheckEnvdVersionForSnapshot(envdVersion string) error {
	ok, err := IsGTEVersion(envdVersion, MinEnvdVersionForSnapshot)
	if err != nil {
		return fmt.Errorf("invalid envd version %q: %w", envdVersion, err)
	}

	if !ok {
		return fmt.Errorf("sandbox envd version must be at least %s to create snapshots, current version: %s", MinEnvdVersionForSnapshot, envdVersion)
	}

	return nil
}

func IsGTEVersion(curVersion, minVersion string) (bool, error) {
	curVersion = sanitizeVersion(curVersion)
	minVersion = sanitizeVersion(minVersion)

	if !semver.IsValid(curVersion) {
		return false, fmt.Errorf("invalid current version format: %s", curVersion)
	}

	if !semver.IsValid(minVersion) {
		return false, fmt.Errorf("invalid minimum version format: %s", minVersion)
	}

	return semver.Compare(curVersion, minVersion) >= 0, nil
}

func IsSmallerVersion(curVersion, maxVersionExcluded string) (bool, error) {
	curVersion = sanitizeVersion(curVersion)
	maxVersionExcluded = sanitizeVersion(maxVersionExcluded)

	if !semver.IsValid(curVersion) {
		return false, fmt.Errorf("invalid current version format: %s", curVersion)
	}

	if !semver.IsValid(maxVersionExcluded) {
		return false, fmt.Errorf("invalid maximum version format: %s", maxVersionExcluded)
	}

	return semver.Compare(curVersion, maxVersionExcluded) < 0, nil
}

func IsVersion(curVersion, eqVersion string) bool {
	curVersion = sanitizeVersion(curVersion)
	eqVersion = sanitizeVersion(eqVersion)

	return semver.Compare(curVersion, eqVersion) == 0
}

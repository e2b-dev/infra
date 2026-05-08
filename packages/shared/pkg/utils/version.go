package utils

import (
	"fmt"

	"golang.org/x/mod/semver"
)

const MinEnvdVersionForSnapshot = "0.5.0"

// MinEnvdVersionForInspector is the first envd version that exposes the
// in-guest InspectorService (see packages/envd/spec/inspector). Older
// envd builds fall through to the always-pause checkpoint path even
// when skip_if_unchanged is requested.
const MinEnvdVersionForInspector = "0.5.16"

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

func CheckEnvdVersionForInspector(envdVersion string) error {
	ok, err := IsGTEVersion(envdVersion, MinEnvdVersionForInspector)
	if err != nil {
		return fmt.Errorf("invalid envd version %q: %w", envdVersion, err)
	}

	if !ok {
		return fmt.Errorf("sandbox envd version must be at least %s for inspector, current version: %s", MinEnvdVersionForInspector, envdVersion)
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

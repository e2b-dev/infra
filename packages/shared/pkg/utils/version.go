package utils

import (
	"fmt"

	"golang.org/x/mod/semver"
)

const MinEnvdVersionForSnapshot = "0.5.0"

// MinEnvdVersionForCgroupFreeze is the first envd we trust to freeze user
// cgroups across pause/resume. /freeze plus /init thaw landed in 0.6.0, but
// 0.6.0-0.6.2 also froze the socat cgroup, breaking port forwarding (fixed
// in 0.6.3 by #2923), so we gate on 0.6.3.
const MinEnvdVersionForCgroupFreeze = "0.6.3"

// MinEnvdVersionForHeapCollapse is the first envd that exposes a native
// POST /collapse endpoint, which compacts envd's own anonymous heap into 2 MiB
// hugepages before pause to reduce the frames it faults on resume. 0.6.4
// already exists in the fleet without /collapse, so the gate must be 0.6.5 (the
// version that introduces the endpoint) to avoid POSTing /collapse at a 0.6.4
// envd that 404s.
const MinEnvdVersionForHeapCollapse = "0.6.5"

// MinEnvdVersionForFsFreeze is the first envd that exposes the native
// POST /fsfreeze and /fsthaw endpoints, which quiesce the guest rootfs before a
// filesystem-only pause. Older envds fall back to a plain guest sync.
const MinEnvdVersionForFsFreeze = "0.6.6"

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

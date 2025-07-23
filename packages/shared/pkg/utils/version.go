package utils

import (
	"fmt"

	"golang.org/x/mod/semver"
)

func IsGTEVersion(curVersion, minVersion string) (bool, error) {
	if len(curVersion) > 0 && curVersion[0] != 'v' {
		curVersion = "v" + curVersion
	}

	if len(minVersion) > 0 && minVersion[0] != 'v' {
		minVersion = "v" + minVersion
	}

	if !semver.IsValid(curVersion) {
		return false, fmt.Errorf("invalid current version format: %s", curVersion)
	}

	if !semver.IsValid(minVersion) {
		return false, fmt.Errorf("invalid minimum version format: %s", minVersion)
	}

	return semver.Compare(curVersion, minVersion) >= 0, nil
}

package utils

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/mod/semver"
)

const (
	MinEnvdVersionForSnapshot = "0.5.0"
	MinEnvdVersionForVolumes  = "0.5.8"
)

func sanitizeVersion(version string) string {
	if len(version) > 0 && version[0] != 'v' {
		version = "v" + version
	}

	return version
}

func DoesEnvdSupportVolumes(ctx context.Context, envdVersion string) bool {
	ok, err := IsGTEVersion(envdVersion, MinEnvdVersionForVolumes)
	if err != nil {
		logger.L().Warn(ctx, "failed to check envd version", zap.Error(err), zap.String("envd_version", envdVersion))
		return false
	}

	return ok
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

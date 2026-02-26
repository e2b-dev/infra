package utils

import (
	"encoding/json"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
)

func MapBuildStatusFromDBStatus(status dbtypes.BuildStatus) api.BuildStatus {
	switch status {
	case dbtypes.BuildStatusFailed:
		return api.BuildStatusFailed
	case dbtypes.BuildStatusUploaded, dbtypes.BuildStatusSuccess:
		return api.BuildStatusSuccess
	default:
		return api.BuildStatusBuilding
	}
}

func MapBuildStatusFromDBStatusGroup(status dbtypes.BuildStatusGroup) api.BuildStatus {
	switch status {
	case dbtypes.BuildStatusGroupFailed:
		return api.BuildStatusFailed
	case dbtypes.BuildStatusGroupReady:
		return api.BuildStatusSuccess
	default:
		return api.BuildStatusBuilding
	}
}

func MapBuildStatusMessageFromDBStatus(status dbtypes.BuildStatus, reason []byte) *string {
	if status != dbtypes.BuildStatusFailed {
		return nil
	}

	return mapBuildStatusMessage(reason)
}

func MapBuildStatusMessageFromDBStatusGroup(status dbtypes.BuildStatusGroup, reason []byte) *string {
	if status != dbtypes.BuildStatusGroupFailed {
		return nil
	}

	return mapBuildStatusMessage(reason)
}

func mapBuildStatusMessage(reason []byte) *string {
	if len(reason) == 0 {
		return nil
	}

	var buildReason dbtypes.BuildReason
	if err := json.Unmarshal(reason, &buildReason); err != nil {
		return nil
	}
	if buildReason.Message == "" {
		return nil
	}

	return &buildReason.Message
}

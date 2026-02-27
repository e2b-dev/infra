package utils

import (
	"encoding/json"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
)

var defaultBuildStatusGroups = []dbtypes.BuildStatusGroup{
	dbtypes.BuildStatusGroupFailed,
	dbtypes.BuildStatusGroupInProgress,
	dbtypes.BuildStatusGroupReady,
	dbtypes.BuildStatusGroupPending,
}

func MapBuildStatusesToDBStatusGroups(statuses *api.BuildStatuses) []dbtypes.BuildStatusGroup {
	if statuses == nil || len(*statuses) == 0 {
		return append([]dbtypes.BuildStatusGroup(nil), defaultBuildStatusGroups...)
	}

	seen := make(map[dbtypes.BuildStatusGroup]struct{}, len(*statuses))
	groups := make([]dbtypes.BuildStatusGroup, 0, len(*statuses))

	for _, status := range *statuses {
		for _, group := range mapBuildStatusToDBStatusGroups(status) {
			if _, exists := seen[group]; exists {
				continue
			}

			seen[group] = struct{}{}
			groups = append(groups, group)
		}
	}

	if len(groups) == 0 {
		return append([]dbtypes.BuildStatusGroup(nil), defaultBuildStatusGroups...)
	}

	return groups
}

func mapBuildStatusToDBStatusGroups(status api.BuildStatus) []dbtypes.BuildStatusGroup {
	switch status {
	case api.Failed:
		return []dbtypes.BuildStatusGroup{dbtypes.BuildStatusGroupFailed}
	case api.Success:
		return []dbtypes.BuildStatusGroup{dbtypes.BuildStatusGroupReady}
	case api.Building:
		return []dbtypes.BuildStatusGroup{
			dbtypes.BuildStatusGroupPending,
			dbtypes.BuildStatusGroupInProgress,
		}
	default:
		return nil
	}
}

func MapBuildStatusFromDBStatus(status dbtypes.BuildStatus) api.BuildStatus {
	switch status {
	case dbtypes.BuildStatusFailed:
		return api.Failed
	case dbtypes.BuildStatusUploaded, dbtypes.BuildStatusSuccess:
		return api.Success
	default:
		return api.Building
	}
}

func MapBuildStatusFromDBStatusGroup(status dbtypes.BuildStatusGroup) api.BuildStatus {
	switch status {
	case dbtypes.BuildStatusGroupFailed:
		return api.Failed
	case dbtypes.BuildStatusGroupReady:
		return api.Success
	default:
		return api.Building
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

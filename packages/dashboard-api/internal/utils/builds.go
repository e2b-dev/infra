package utils

import (
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

func MapBuildStatusMessageFromDBStatus(status dbtypes.BuildStatus, reason dbtypes.BuildReason) *string {
	if status != dbtypes.BuildStatusFailed {
		return nil
	}

	if reason.Message == "" {
		return nil
	}

	return &reason.Message
}

func MapBuildStatusMessageFromDBStatusGroup(status dbtypes.BuildStatusGroup, reason dbtypes.BuildReason) *string {
	if status != dbtypes.BuildStatusGroupFailed {
		return nil
	}

	if reason.Message == "" {
		return nil
	}

	return &reason.Message
}

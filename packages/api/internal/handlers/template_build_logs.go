package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (a *APIStore) GetTemplatesTemplateIDBuildsBuildIDLogs(c *gin.Context, templateID api.TemplateID, buildID api.BuildID, params api.GetTemplatesTemplateIDBuildsBuildIDLogsParams) {
	ctx := c.Request.Context()

	buildUUID, err := uuid.Parse(buildID)
	if err != nil {
		a.sendAPIStoreError(ctx, c, http.StatusBadRequest, "Invalid build id", err)

		return
	}

	buildInfo, err := a.templateBuildsCache.Get(ctx, buildUUID, templateID)
	if err != nil {
		var notFoundErr templatecache.TemplateBuildInfoNotFoundError
		if errors.As(err, &notFoundErr) {
			a.sendAPIStoreError(ctx, c, http.StatusBadRequest, fmt.Sprintf("Build '%s' not found", buildUUID), err)

			return
		}

		a.sendAPIStoreError(ctx, c, http.StatusInternalServerError, "Error when getting template", err)

		return
	}

	infoTeamID := buildInfo.TeamID.String()
	team, apiErr := a.GetTeam(ctx, c, &infoTeamID)
	if apiErr != nil {
		a.sendAPIStoreError(ctx, c, apiErr.Code, apiErr.ClientMsg, apiErr.Err)

		return
	}

	ctx = telemetry.WithAttributes(ctx,
		telemetry.WithTeamID(team.ID.String()),
		telemetry.WithBuildID(buildUUID.String()),
		telemetry.WithTemplateID(templateID),
	)

	if team.ID != buildInfo.TeamID {
		a.sendAPIStoreError(ctx, c, http.StatusForbidden, fmt.Sprintf("You don't have access to this sandbox template (%s)", templateID), nil)

		return
	}

	// early return if still waiting for build start
	if buildInfo.BuildStatus == types.BuildStatusWaiting {
		c.JSON(http.StatusOK, api.TemplateBuildLogsResponse{
			Logs: []api.BuildLogEntry{},
		})

		return
	}

	cluster, ok := a.clusters.GetClusterById(utils.WithClusterFallback(team.ClusterID))
	if !ok {
		a.sendAPIStoreError(ctx, c, http.StatusInternalServerError, "Error when getting cluster", nil)

		return
	}

	limit := maxLogEntriesPerRequest
	if params.Limit != nil && *params.Limit < maxLogEntriesPerRequest {
		limit = *params.Limit
	}

	direction := api.LogsDirectionForward
	if params.Direction != nil && *params.Direction == api.LogsDirectionBackward {
		direction = api.LogsDirectionBackward
	}

	var cursor *time.Time
	if params.Cursor != nil {
		cursor = sharedUtils.ToPtr(time.UnixMilli(*params.Cursor))
	}

	logs, apiErr := cluster.GetResources().GetBuildLogs(ctx, buildInfo.NodeID, templateID, buildID, 0, limit, apiToLogLevel(params.Level), cursor, direction, params.Source)
	if apiErr != nil {
		a.sendAPIStoreError(ctx, c, apiErr.Code, apiErr.ClientMsg, apiErr.Err)

		return
	}

	logEntries := make([]api.BuildLogEntry, len(logs))
	for i, entry := range logs {
		logEntries[i] = getAPILogEntry(entry)
	}

	c.JSON(http.StatusOK, api.TemplateBuildLogsResponse{Logs: logEntries})
}

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
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/templates"
	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const maxLogEntriesPerRequest = int32(100)

// GetTemplatesTemplateIDBuildsBuildIDStatus serves to get a template build status (e.g. to CLI)
func (a *APIStore) GetTemplatesTemplateIDBuildsBuildIDStatus(c *gin.Context, templateID api.TemplateID, buildID api.BuildID, params api.GetTemplatesTemplateIDBuildsBuildIDStatusParams) {
	ctx := c.Request.Context()

	buildUUID, err := uuid.Parse(buildID)
	if err != nil {
		telemetry.ReportError(ctx, "error when parsing build id", err)
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid build id")

		return
	}

	buildInfo, err := a.templateBuildsCache.Get(ctx, buildUUID, templateID)
	if err != nil {
		var notFoundErr templatecache.TemplateBuildInfoNotFoundError
		if errors.As(err, &notFoundErr) {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Build '%s' not found", buildUUID))

			return
		}

		telemetry.ReportError(ctx, "error when getting template", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting template")

		return
	}

	infoTeamID := buildInfo.TeamID.String()
	team, apiErr := a.GetTeam(ctx, c, &infoTeamID)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team and tier", apiErr.Err)

		return
	}

	if team.ID != buildInfo.TeamID {
		telemetry.ReportError(ctx, "user doesn't have access to env", fmt.Errorf("user doesn't have access to env '%s'", templateID), telemetry.WithTemplateID(templateID))
		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You don't have access to this sandbox template (%s)", templateID))

		return
	}

	// early return if still waiting for build start
	if buildInfo.BuildStatus == types.BuildStatusWaiting {
		result := api.TemplateBuildInfo{
			LogEntries: make([]api.BuildLogEntry, 0),
			Logs:       make([]string, 0),
			TemplateID: templateID,
			BuildID:    buildID,
			Status:     api.TemplateBuildStatusWaiting,
		}

		c.JSON(http.StatusOK, result)

		return
	}

	// Needs to be before logs request so the status is not set to done too early
	result := api.TemplateBuildInfo{
		LogEntries: nil,
		Logs:       nil,
		TemplateID: templateID,
		BuildID:    buildID,
		Status:     getCorrespondingTemplateBuildStatus(buildInfo.BuildStatus),
		Reason:     getAPIReason(buildInfo.Reason),
	}

	lgs := make([]string, 0)
	logEntries := make([]api.BuildLogEntry, 0)
	offset := int32(0)
	if params.LogsOffset != nil {
		offset = *params.LogsOffset
	}

	// Check if we need to return legacy logs format too, used only for the v1 template builds in the CLI
	cv := sharedUtils.DerefOrDefault(buildInfo.Version, templates.TemplateV1Version)
	legacyLogs, err := sharedUtils.IsSmallerVersion(cv, templates.TemplateV2BetaVersion)
	if err != nil {
		telemetry.ReportError(ctx, "error when comparing versions", err, telemetry.WithTemplateID(templateID), telemetry.WithBuildID(buildID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when processing build logs")

		return
	}

	cluster, ok := a.clusters.GetClusterById(utils.WithClusterFallback(team.ClusterID))
	if !ok {
		telemetry.ReportError(ctx, "error when getting cluster", fmt.Errorf("cluster with ID '%s' not found", team.ClusterID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting cluster")

		return
	}

	limit := maxLogEntriesPerRequest
	if params.Limit != nil && *params.Limit < maxLogEntriesPerRequest {
		limit = *params.Limit
	}

	logs, apiErr := cluster.GetResources().GetBuildLogs(ctx, buildInfo.NodeID, templateID, buildID, offset, limit, apiToLogLevel(params.Level), nil, api.LogsDirectionForward, nil)
	if apiErr != nil {
		telemetry.ReportCriticalError(ctx, "error when getting build logs", apiErr.Err, telemetry.WithTemplateID(templateID), telemetry.WithBuildID(buildID))
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	for _, entry := range logs {
		if legacyLogs {
			lgs = append(lgs, fmt.Sprintf("[%s] %s\n", entry.Timestamp.Format(time.RFC3339), entry.Message))
		}
		logEntries = append(logEntries, getAPILogEntry(entry))
	}

	result.Logs = lgs
	result.LogEntries = logEntries

	if result.Reason != nil && result.Reason.Step != nil {
		result.Reason.LogEntries = sharedUtils.ToPtr(filterStepLogs(logEntries, *result.Reason.Step, api.LogLevelWarn))
	}

	c.JSON(http.StatusOK, result)
}

func getCorrespondingTemplateBuildStatus(s types.BuildStatus) api.TemplateBuildStatus {
	switch s {
	case types.BuildStatusWaiting:
		return api.TemplateBuildStatusWaiting
	case types.BuildStatusFailed:
		return api.TemplateBuildStatusError
	case types.BuildStatusUploaded:
		return api.TemplateBuildStatusReady
	default:
		return api.TemplateBuildStatusBuilding
	}
}

func getAPIReason(reason types.BuildReason) *api.BuildStatusReason {
	if reason.Message == "" {
		return nil
	}

	return &api.BuildStatusReason{
		Message:    reason.Message,
		Step:       reason.Step,
		LogEntries: nil,
	}
}

func filterStepLogs(logEntries []api.BuildLogEntry, step string, minLevel api.LogLevel) []api.BuildLogEntry {
	return sharedUtils.Filter(logEntries, func(line api.BuildLogEntry) bool {
		return logs.CompareLevels(string(line.Level), string(minLevel)) >= 0 && line.Step != nil && *line.Step == step
	})
}

func getAPILogEntry(entry logs.LogEntry) api.BuildLogEntry {
	stepField := entry.Fields["step"]

	var step *string
	if stepField != "" {
		step = &stepField
	}

	return api.BuildLogEntry{
		Timestamp: entry.Timestamp,
		Message:   entry.Message,
		Level:     api.LogLevel(logs.LevelToString(entry.Level)),
		Step:      step,
	}
}

func apiToLogLevel(level *api.LogLevel) *logs.LogLevel {
	if level == nil {
		return nil
	}

	value := logs.StringToLevel(string(*level))

	return &value
}

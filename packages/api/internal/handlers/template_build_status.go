package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

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
		if errors.Is(err, db.TemplateBuildNotFound{}) {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Build '%s' not found", buildUUID))
			return
		}

		if errors.Is(err, db.TemplateNotFound{}) {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Template '%s' not found", templateID))
			return
		}

		telemetry.ReportError(ctx, "error when getting template", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting template")
		return
	}

	infoTeamID := buildInfo.TeamID.String()
	team, _, apiErr := a.GetTeamAndTier(c, &infoTeamID)
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
	if buildInfo.BuildStatus == envbuild.StatusWaiting {
		result := api.TemplateBuild{
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
	result := api.TemplateBuild{
		LogEntries: nil,
		Logs:       nil,
		TemplateID: templateID,
		BuildID:    buildID,
		Status:     getCorrespondingTemplateBuildStatus(buildInfo.BuildStatus),
		Reason:     buildInfo.Reason,
	}

	cli, err := a.templateManager.GetBuildClient(team.ClusterID, buildInfo.ClusterNodeID, false)
	if err != nil {
		telemetry.ReportError(ctx, "error when getting build client", err, telemetry.WithTemplateID(templateID), telemetry.WithBuildID(buildID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting build client")
		return
	}

	lgs := make([]string, 0)
	logEntries := make([]api.BuildLogEntry, 0)
	offset := int32(0)
	if params.LogsOffset != nil {
		offset = *params.LogsOffset
	}

	for _, entry := range cli.GetLogs(ctx, templateID, buildID, offset, apiToLogLevel(params.Level)) {
		lgs = append(lgs, fmt.Sprintf("[%s] %s\n", entry.Timestamp.Format(time.RFC3339), entry.Message))
		logEntries = append(logEntries, api.BuildLogEntry{
			Timestamp: entry.Timestamp,
			Message:   entry.Message,
			Level:     api.LogLevel(logs.LevelToString(entry.Level)),
		})
	}

	result.Logs = lgs
	result.LogEntries = logEntries

	c.JSON(http.StatusOK, result)
}

func getCorrespondingTemplateBuildStatus(s envbuild.Status) api.TemplateBuildStatus {
	switch s {
	case envbuild.StatusWaiting:
		return api.TemplateBuildStatusWaiting
	case envbuild.StatusFailed:
		return api.TemplateBuildStatusError
	case envbuild.StatusUploaded:
		return api.TemplateBuildStatusReady
	default:
		return api.TemplateBuildStatusBuilding
	}
}

func apiToLogLevel(level *api.LogLevel) *logs.LogLevel {
	if level == nil {
		return nil
	}

	value := logs.StringToLevel(string(*level))
	return &value
}

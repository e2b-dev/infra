package handlers

import (
	"encoding/json"
	"fmt"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/grafana/loki/pkg/loghttp"
	"github.com/grafana/loki/pkg/logproto"
	"go.uber.org/zap"
	"net/http"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	templateBuildLogsLimit       = 1_000
	templateBuildOldestLogsLimit = 24 * time.Hour // 1 day
)

// GetTemplatesTemplateIDBuildsBuildIDStatus serves to get a template build status (e.g. to CLI)
func (a *APIStore) GetTemplatesTemplateIDBuildsBuildIDStatus(c *gin.Context, templateID api.TemplateID, buildID api.BuildID, params api.GetTemplatesTemplateIDBuildsBuildIDStatusParams) {
	ctx := c.Request.Context()

	userID := c.Value(auth.UserIDContextKey).(uuid.UUID)
	teams, err := a.db.GetTeams(ctx, userID)
	if err != nil {
		errMsg := fmt.Errorf("error when getting teams: %w", err)

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to get the default team")

		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	buildUUID, err := uuid.Parse(buildID)
	if err != nil {
		errMsg := fmt.Errorf("error when parsing build id: %w", err)
		telemetry.ReportError(ctx, errMsg)

		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid build id")

		return
	}

	dockerBuild, err := a.buildCache.Get(templateID, buildUUID)
	if err != nil {
		msg := fmt.Errorf("error finding cache for env %s and build %s", templateID, buildID)
		telemetry.ReportError(ctx, msg)

		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Build (%s) not found", buildID))

		return
	}

	templateTeamID := dockerBuild.GetTeamID()

	var team *models.Team
	for _, t := range teams {
		if t.ID == templateTeamID {
			team = t
			break
		}
	}

	if team == nil {
		msg := fmt.Errorf("user doesn't have access to env '%s'", templateID)
		telemetry.ReportError(ctx, msg)

		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You don't have access to this sandbox template (%s)", templateID))

		return
	}

	status := dockerBuild.GetStatus()

	query := fmt.Sprintf("{source=\"logs-collector\", service=\"template-manager\", buildID=\"%s\", envID=\"%s\"}", buildUUID.String(), templateID)
	end := time.Now()
	start := end.Add(-templateBuildOldestLogsLimit)

	res, err := a.lokiClient.QueryRange(query, templateBuildLogsLimit, start, end, logproto.FORWARD, time.Duration(0), time.Duration(0), true)
	if err != nil {
		errMsg := fmt.Errorf("error when returning logs for template build: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error returning logs for template build '%s'", buildUUID.String()))
		return
	}

	logs := make([]string, 0)
	logsSkippedOffset := 0

	switch res.Data.Result.Type() {
	case loghttp.ResultTypeStream:
		value := res.Data.Result.(loghttp.Streams)

		for _, stream := range value {
			for _, entry := range stream.Entries {
				logsSkippedOffset++

				// loki does not support offset pagination, so we need to skip logs manually
				if logsSkippedOffset < int(*params.LogsOffset) {
					continue
				}

				line := make(map[string]interface{})
				err := json.Unmarshal([]byte(entry.Line), &line)
				if err != nil {
					zap.L().Error("error parsing log line", zap.Error(err), zap.String("buildID", buildID), zap.String("line", entry.Line))
				}

				logs = append(logs, line["message"].(string))
			}
		}
	}

	result := api.TemplateBuild{
		Logs:       logs,
		TemplateID: templateID,
		BuildID:    buildID,
		Status:     status,
	}

	c.JSON(http.StatusOK, result)
}

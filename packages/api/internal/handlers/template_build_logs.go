package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/grafana/loki/pkg/loghttp"
	"github.com/grafana/loki/pkg/logproto"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
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

	// Sanitize env ID
	// https://grafana.com/blog/2021/01/05/how-to-escape-special-characters-with-lokis-logql/
	templateIdSanitized := strings.ReplaceAll(templateID, "`", "")
	query := fmt.Sprintf("{service=\"template-manager\", buildID=\"%s\", envID=`%s`}", buildUUID.String(), templateIdSanitized)

	end := time.Now()
	start := end.Add(-templateBuildOldestLogsLimit)

	res, err := a.lokiClient.QueryRange(query, templateBuildLogsLimit, start, end, logproto.FORWARD, time.Duration(0), time.Duration(0), true)
	if err != nil {
		errMsg := fmt.Errorf("error when returning logs for template build: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
		zap.L().Error("error when returning logs for template build", zap.Error(err), zap.String("buildID", buildID))

		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error returning logs for template build '%s'", buildUUID.String()))
		return
	}

	logs := make([]string, 0)
	logsCrawled := 0

	offset := 0
	if params.LogsOffset != nil {
		offset = int(*params.LogsOffset)
	}

	if res.Data.Result.Type() != loghttp.ResultTypeStream {
		zap.L().Error("unexpected value type received from loki query fetch", zap.String("type", string(res.Data.Result.Type())))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Unexpected error during fetching logs")
		return
	}

	for _, stream := range res.Data.Result.(loghttp.Streams) {
		for _, entry := range stream.Entries {
			logsCrawled++

			// loki does not support offset pagination, so we need to skip logs manually
			if logsCrawled <= offset {
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

	result := api.TemplateBuild{
		Logs:       logs,
		TemplateID: templateID,
		BuildID:    buildID,
		Status:     status,
	}

	c.JSON(http.StatusOK, result)
}

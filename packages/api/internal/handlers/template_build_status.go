package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/grafana/loki/pkg/loghttp"
	"github.com/grafana/loki/pkg/logproto"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
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
	teams, err := a.sqlcDB.GetTeamsWithUsersTeams(ctx, userID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to get the default team")

		telemetry.ReportCriticalError(ctx, "error when getting teams", err)

		return
	}

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

	var team *queries.Team
	for _, t := range teams {
		if t.Team.ID == buildInfo.TeamID {
			team = &t.Team
			break
		}
	}

	if team == nil {
		telemetry.ReportError(ctx, "user doesn't have access to env", fmt.Errorf("user doesn't have access to env '%s'", templateID), telemetry.WithTemplateID(templateID))
		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You don't have access to this sandbox template (%s)", templateID))
		return
	}

	// early return if still waiting for build start
	if buildInfo.BuildStatus == envbuild.StatusWaiting {
		result := api.TemplateBuild{
			Logs:       make([]string, 0),
			TemplateID: templateID,
			BuildID:    buildID,
			Status:     api.TemplateBuildStatusWaiting,
		}

		c.JSON(http.StatusOK, result)
		return
	}

	// Sanitize env ID
	// https://grafana.com/blog/2021/01/05/how-to-escape-special-characters-with-lokis-logql/
	templateIdSanitized := strings.ReplaceAll(templateID, "`", "")
	query := fmt.Sprintf("{service=\"template-manager\", buildID=\"%s\", envID=`%s`}", buildUUID.String(), templateIdSanitized)

	end := time.Now()
	start := end.Add(-templateBuildOldestLogsLimit)
	logs := make([]string, 0)

	res, err := a.lokiClient.QueryRange(query, templateBuildLogsLimit, start, end, logproto.FORWARD, time.Duration(0), time.Duration(0), true)
	if err == nil {
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
					zap.L().Error("error parsing log line", zap.Error(err), logger.WithBuildID(buildID), zap.String("line", entry.Line))
				}

				logs = append(logs, line["message"].(string))
			}
		}
	} else {
		telemetry.ReportError(ctx, "error when returning logs for template build", err)
		zap.L().Error("error when returning logs for template build", zap.Error(err), zap.String("buildID", buildID))
	}

	result := api.TemplateBuild{
		Logs:       logs,
		TemplateID: templateID,
		BuildID:    buildID,
		Status:     getCorrespondingTemplateBuildStatus(buildInfo.BuildStatus),
	}

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

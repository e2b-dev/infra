package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
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

	// Needs to be before logs request so the status is not set to done too early
	result := api.TemplateBuild{
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

	logs := make([]string, 0)
	for _, entry := range cli.GetLogs(ctx, templateID, buildID, params.LogsOffset) {
		out, err := json.Marshal(entry)
		if err != nil {
			telemetry.ReportError(ctx, "error when marshalling log entry", err, telemetry.WithTemplateID(templateID), telemetry.WithBuildID(buildID))
			a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when processing log entry")
			return
		}
		logs = append(logs, string(out)+"\n")
	}

	result.Logs = logs

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

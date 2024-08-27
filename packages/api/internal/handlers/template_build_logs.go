package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
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

	telemetry.SetAttributes(ctx,
		attribute.String("user.id", userID.String()),
		attribute.String("env.id", templateID),
	)

	buildUUID, err := uuid.Parse(buildID)
	if err != nil {
		errMsg := fmt.Errorf("error when parsing build id: %w", err)

		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid build id")

		telemetry.ReportError(ctx, errMsg)

		return
	}

	dockerBuild, err := a.buildCache.Get(templateID, buildUUID)
	if err != nil {
		msg := fmt.Errorf("error finding cache for env %s and build %s", templateID, buildID)

		a.sendAPIStoreError(c, http.StatusNotFound, "Build not found")

		telemetry.ReportError(ctx, msg)

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

		a.sendAPIStoreError(c, http.StatusForbidden, "You don't have access to this sandbox template")

		telemetry.ReportError(ctx, msg)

		return
	}

	telemetry.SetAttributes(ctx, attribute.String("team.id", team.ID.String()))

	status := dockerBuild.GetStatus()
	logs := dockerBuild.GetLogs()

	result := api.TemplateBuild{
		Logs:       logs[*params.LogsOffset:],
		TemplateID: templateID,
		BuildID:    buildID,
		Status:     status,
	}

	telemetry.ReportEvent(ctx, "got template build status")
	c.JSON(http.StatusOK, result)
}

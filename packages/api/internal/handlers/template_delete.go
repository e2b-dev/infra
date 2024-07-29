package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// DeleteTemplatesTemplateID serves to delete an env (e.g. in CLI)
func (a *APIStore) DeleteTemplatesTemplateID(c *gin.Context, aliasOrTemplateID api.TemplateID) {
	ctx := c.Request.Context()

	cleanedAliasOrEnvID, err := utils.CleanEnvID(aliasOrTemplateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid env ID: %s", aliasOrTemplateID))

		err = fmt.Errorf("invalid env ID: %w", err)
		telemetry.ReportCriticalError(ctx, err)

		return
	}

	// Prepare info for rebuilding env
	userID, teams, err := a.GetUserAndTeams(c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting default team: %s", err))

		err = fmt.Errorf("error when getting default team: %w", err)
		telemetry.ReportCriticalError(ctx, err)

		return
	}

	env, build, err := a.db.GetEnv(ctx, cleanedAliasOrEnvID)
	if err != nil {
		errMsg := fmt.Errorf("error env not found: %w", err)
		telemetry.ReportError(ctx, errMsg)

		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("the sandbox template '%s' wasn't found", cleanedAliasOrEnvID))

		return
	}

	var team *models.Team
	for _, t := range teams {
		if t.ID == env.TeamID {
			team = t
			break
		}
	}

	if team == nil {
		errMsg := fmt.Errorf("user '%s' doesn't have access to the sandbox template '%s'", cleanedAliasOrEnvID)
		telemetry.ReportError(ctx, errMsg)

		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox template '%s'", cleanedAliasOrEnvID))

		return
	}

	telemetry.SetAttributes(ctx,
		attribute.String("user.id", userID.String()),
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.team.name", team.Name),
		attribute.String("env.id", env.TemplateID),
		attribute.String("env.kernel.version", build.KernelVersion),
		attribute.String("env.firecracker.version", build.FirecrackerVersion),
	)

	deleteJobErr := a.templateManager.DeleteInstance(ctx, env.TemplateID)
	if deleteJobErr != nil {
		errMsg := fmt.Errorf("error when deleting env files from fc-envs disk: %w", deleteJobErr)
		telemetry.ReportCriticalError(ctx, errMsg)
	} else {
		telemetry.ReportEvent(ctx, "deleted env from fc-envs disk")
	}

	dbErr := a.db.DeleteEnv(ctx, env.TemplateID)
	if dbErr != nil {
		errMsg := fmt.Errorf("error when deleting env from db: %w", dbErr)
		telemetry.ReportCriticalError(ctx, errMsg)

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when deleting env")

		return
	}

	a.templateCache.Invalidate(env.TemplateID)

	telemetry.ReportEvent(ctx, "deleted env from db")

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsUserEvent(userID.String(), team.ID.String(), "deleted environment", properties.Set("environment", env.TemplateID))

	a.logger.Infof("Deleted env '%s' from team '%s'", env.TemplateID, team.ID)

	c.JSON(http.StatusOK, nil)
}

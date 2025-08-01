package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/posthog/posthog-go"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	template_manager "github.com/e2b-dev/infra/packages/api/internal/template-manager"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// CheckAndCancelConcurrentBuilds checks for concurrent builds and cancels them if found
func (a *APIStore) CheckAndCancelConcurrentBuilds(ctx context.Context, templateID api.TemplateID, buildID uuid.UUID) error {
	concurrentlyRunningBuilds, err := a.db.
		Client.
		EnvBuild.
		Query().
		Where(
			envbuild.EnvID(templateID),
			envbuild.StatusIn(envbuild.StatusWaiting, envbuild.StatusBuilding),
			envbuild.IDNotIn(buildID),
		).
		All(ctx)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "Error when getting running builds", err)
		return fmt.Errorf("error when getting running builds: %w", err)
	}

	// make sure there is no other build in progress for the same template
	if len(concurrentlyRunningBuilds) > 0 {
		buildIDs := utils.Map(concurrentlyRunningBuilds, func(b *models.EnvBuild) template_manager.DeleteBuild {
			return template_manager.DeleteBuild{
				TemplateID: templateID,
				BuildID:    b.ID,
			}
		})
		telemetry.ReportEvent(ctx, "canceling running builds", attribute.StringSlice("ids", utils.Map(buildIDs, func(b template_manager.DeleteBuild) string {
			return fmt.Sprintf("%s/%s", b.TemplateID, b.BuildID)
		})))
		deleteJobErr := a.templateManager.DeleteBuilds(ctx, buildIDs)
		if deleteJobErr != nil {
			telemetry.ReportCriticalError(ctx, "error when canceling running build", deleteJobErr)
			return fmt.Errorf("error when canceling running build: %w", deleteJobErr)
		}
		telemetry.ReportEvent(ctx, "canceled running builds")
	}

	return nil
}

// PostTemplatesTemplateIDBuildsBuildID triggers a new build after the user pushes the Docker image to the registry
func (a *APIStore) PostTemplatesTemplateIDBuildsBuildID(c *gin.Context, templateID api.TemplateID, buildID api.BuildID) {
	ctx := c.Request.Context()

	buildUUID, err := uuid.Parse(buildID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid build ID: %s", buildID))

		telemetry.ReportCriticalError(ctx, "invalid build ID", err)

		return
	}

	userID, teams, err := a.GetUserAndTeams(c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting default team: %s", err))

		telemetry.ReportCriticalError(ctx, "error when getting default team", err)

		return
	}

	telemetry.ReportEvent(ctx, "started environment build")

	// Check if the user has access to the template, load the template with build info
	templateBuildDB, err := a.sqlcDB.GetTemplateBuild(ctx, queries.GetTemplateBuildParams{
		TemplateID: templateID,
		BuildID:    buildUUID,
	})
	if err != nil {
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error when getting template: %s", err))

		telemetry.ReportCriticalError(ctx, "error when getting env", err, telemetry.WithTemplateID(templateID))

		return
	}

	var team *queries.Team
	// Check if the user has access to the template
	for _, t := range teams {
		if t.Team.ID == templateBuildDB.Env.TeamID {
			team = &t.Team
			break
		}
	}

	if team == nil {
		a.sendAPIStoreError(c, http.StatusForbidden, "User does not have access to the template")

		telemetry.ReportCriticalError(ctx, "user does not have access to the template", err, telemetry.WithTemplateID(templateID))

		return
	}

	telemetry.SetAttributes(ctx,
		attribute.String("user.id", userID.String()),
		telemetry.WithTeamID(team.ID.String()),
		telemetry.WithTemplateID(templateID),
	)

	// Check and cancel concurrent builds
	if err := a.CheckAndCancelConcurrentBuilds(ctx, templateID, buildUUID); err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error during template build request")
		return
	}

	startTime := time.Now()
	build := templateBuildDB.EnvBuild

	// only waiting builds can be triggered
	if build.Status != envbuild.StatusWaiting.String() {
		a.sendAPIStoreError(c, http.StatusBadRequest, "build is not in waiting state")
		telemetry.ReportCriticalError(ctx, "build is not in waiting state", fmt.Errorf("build is not in waiting state: %s", build.Status), telemetry.WithTemplateID(templateID))
		return
	}

	// team is part of the cluster but template build is not assigned to a cluster node so its invalid stats
	if team.ClusterID != nil && build.ClusterNodeID == nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "build is not assigned to a cluster node")
		telemetry.ReportCriticalError(ctx, "build is not assigned to a cluster node", nil, telemetry.WithTemplateID(templateID))
		return
	}

	// Call the Template Manager to build the environment
	forceRebuild := true
	buildErr := a.templateManager.CreateTemplate(
		a.Tracer,
		ctx,
		team.ID,
		templateID,
		buildUUID,
		build.KernelVersion,
		build.FirecrackerVersion,
		build.StartCmd,
		build.Vcpu,
		build.FreeDiskSizeMb,
		build.RamMb,
		build.ReadyCmd,
		"",
		&forceRebuild,
		nil,
		team.ClusterID,
		build.ClusterNodeID,
	)
	if buildErr != nil {
		telemetry.ReportCriticalError(ctx, "build failed", buildErr, telemetry.WithTemplateID(templateID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when starting template build: %s", buildErr))
		return
	}

	a.posthog.CreateAnalyticsUserEvent(userID.String(), team.ID.String(), "built environment", posthog.NewProperties().
		Set("user_id", userID).
		Set("environment", templateID).
		Set("build_id", buildID).
		Set("duration", time.Since(startTime).String()).
		Set("success", err != nil),
	)

	c.Status(http.StatusAccepted)
}

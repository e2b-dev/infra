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
	dbapi "github.com/e2b-dev/infra/packages/api/internal/db"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	templatemanager "github.com/e2b-dev/infra/packages/api/internal/template-manager"
	apiutils "github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/templates"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// CheckAndCancelConcurrentBuilds checks for concurrent builds and cancels them if found
func (a *APIStore) CheckAndCancelConcurrentBuilds(ctx context.Context, templateID api.TemplateID, buildID uuid.UUID, teamClusterID uuid.UUID) error {
	concurrentlyRunningBuilds, err := a.sqlcDB.GetConcurrentTemplateBuilds(ctx, queries.GetConcurrentTemplateBuildsParams{
		TemplateID:     templateID,
		CurrentBuildID: buildID,
	})
	if err != nil {
		telemetry.ReportCriticalError(ctx, "Error when getting running builds", err)

		return fmt.Errorf("error when getting running builds: %w", err)
	}

	// make sure there is no other build in progress for the same template
	if len(concurrentlyRunningBuilds) > 0 {
		buildIDs := utils.Map(concurrentlyRunningBuilds, func(b queries.EnvBuild) templatemanager.DeleteBuild {
			return templatemanager.DeleteBuild{
				TemplateID: templateID,
				BuildID:    b.ID,
				ClusterID:  teamClusterID,
				NodeID:     b.ClusterNodeID,
			}
		})
		telemetry.ReportEvent(ctx, "canceling running builds", attribute.StringSlice("ids", utils.Map(buildIDs, func(b templatemanager.DeleteBuild) string {
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

	userID := a.GetUserID(c)

	teams, err := dbapi.GetTeamsByUser(ctx, a.sqlcDB, userID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting default team: %s", err))

		telemetry.ReportCriticalError(ctx, "error when getting default team", err)

		return
	}

	telemetry.ReportEvent(ctx, "started environment build")

	// Check if the user has access to the template, load the template with build info
	templateBuildDB, err := a.sqlcDB.GetTemplateBuildWithTemplate(ctx, queries.GetTemplateBuildWithTemplateParams{
		TemplateID: templateID,
		BuildID:    buildUUID,
	})
	if err != nil {
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error when getting template: %s", err))

		telemetry.ReportCriticalError(ctx, "error when getting env", err, telemetry.WithTemplateID(templateID))

		return
	}

	var team *types.Team
	// Check if the user has access to the template
	for _, t := range teams {
		if t.Team.ID == templateBuildDB.Env.TeamID {
			team = t.Team

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
	if err := a.CheckAndCancelConcurrentBuilds(ctx, templateID, buildUUID, apiutils.WithClusterFallback(team.ClusterID)); err != nil {
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

	// Call the Template Manager to build the environment
	forceRebuild := true
	fromImage := ""
	err = a.templateManager.CreateTemplate(
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
		&fromImage,
		nil, // fromTemplate not supported in v1 handler
		nil, // fromImageRegistry not supported in v1 handler
		&forceRebuild,
		nil,
		apiutils.WithClusterFallback(team.ClusterID),
		build.ClusterNodeID,
		templates.TemplateV1Version,
	)

	a.posthog.CreateAnalyticsUserEvent(userID.String(), team.ID.String(), "built environment", posthog.NewProperties().
		Set("user_id", userID).
		Set("environment", templateID).
		Set("build_id", buildID).
		Set("duration", time.Since(startTime).String()).
		Set("success", err == nil),
	)

	if err != nil {
		telemetry.ReportCriticalError(ctx, "build failed", err, telemetry.WithTemplateID(templateID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when starting template build: %s", err))

		return
	}

	c.Status(http.StatusAccepted)
}

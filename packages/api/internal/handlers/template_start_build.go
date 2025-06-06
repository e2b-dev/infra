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
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	template_manager "github.com/e2b-dev/infra/packages/api/internal/template-manager"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// PostTemplatesTemplateIDBuildsBuildID triggers a new build after the user pushes the Docker image to the registry
func (a *APIStore) PostTemplatesTemplateIDBuildsBuildID(c *gin.Context, templateID api.TemplateID, buildID api.BuildID) {
	ctx := c.Request.Context()
	span := trace.SpanFromContext(ctx)

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
	envDB, err := a.db.Client.Env.Query().Where(
		env.ID(templateID),
	).WithBuilds(
		func(query *models.EnvBuildQuery) {
			query.Where(envbuild.ID(buildUUID))
		},
	).Only(ctx)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error when getting template: %s", err))

		telemetry.ReportCriticalError(ctx, "error when getting env", err, attribute.String("template_id", templateID))

		return
	}

	var team *queries.Team
	// Check if the user has access to the template
	for _, t := range teams {
		if t.Team.ID == envDB.TeamID {
			team = &t.Team
			break
		}
	}

	if team == nil {
		a.sendAPIStoreError(c, http.StatusForbidden, "User does not have access to the template")

		telemetry.ReportCriticalError(ctx, "user does not have access to the template", err, attribute.String("template_id", templateID))

		return
	}

	telemetry.SetAttributes(ctx,
		attribute.String("user.id", userID.String()),
		attribute.String("team.id", team.ID.String()),
		attribute.String("template.id", templateID),
	)

	concurrentlyRunningBuilds, err := a.db.
		Client.
		EnvBuild.
		Query().
		Where(
			envbuild.EnvID(envDB.ID),
			envbuild.StatusIn(envbuild.StatusWaiting, envbuild.StatusBuilding),
			envbuild.IDNotIn(buildUUID),
		).
		All(ctx)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error during template build request")
		telemetry.ReportCriticalError(ctx, "Error when getting running builds", err)
		return
	}

	// make sure there is no other build in progress for the same template
	if len(concurrentlyRunningBuilds) > 0 {
		buildIDs := utils.Map(concurrentlyRunningBuilds, func(b *models.EnvBuild) template_manager.DeleteBuild {
			return template_manager.DeleteBuild{
				TemplateId: envDB.ID,
				BuildID:    b.ID,
			}
		})
		telemetry.ReportEvent(ctx, "canceling running builds", attribute.StringSlice("ids", utils.Map(buildIDs, func(b template_manager.DeleteBuild) string {
			return fmt.Sprintf("%s/%s", b.TemplateId, b.BuildID)
		})))
		deleteJobErr := a.templateManager.DeleteBuilds(ctx, buildIDs)
		if deleteJobErr != nil {
			a.sendAPIStoreError(c, http.StatusInternalServerError, "Error during template build cancel request")
			telemetry.ReportCriticalError(ctx, "error when canceling running build", deleteJobErr)
			return
		}
		telemetry.ReportEvent(ctx, "canceled running builds")
	}

	startTime := time.Now()
	build := envDB.Edges.Builds[0]
	var startCmd string
	if build.StartCmd != nil {
		startCmd = *build.StartCmd
	}

	var readyCmd string
	if build.ReadyCmd != nil {
		readyCmd = *build.ReadyCmd
	}

	// only waiting builds can be triggered
	if build.Status != envbuild.StatusWaiting {
		a.sendAPIStoreError(c, http.StatusBadRequest, "build is not in waiting state")
		telemetry.ReportCriticalError(ctx, "build is not in waiting state", fmt.Errorf("build is not in waiting state: %s", build.Status), attribute.String("template_id", templateID))
		return
	}

	// Call the Template Manager to build the environment
	buildErr := a.templateManager.CreateTemplate(
		a.Tracer,
		ctx,
		templateID,
		buildUUID,
		build.KernelVersion,
		build.FirecrackerVersion,
		startCmd,
		build.Vcpu,
		build.FreeDiskSizeMB,
		build.RAMMB,
		readyCmd,
	)

	if buildErr != nil {
		telemetry.ReportCriticalError(ctx, "build failed", buildErr, attribute.String("template_id", templateID))

		err = a.templateManager.SetStatus(
			ctx,
			templateID,
			buildUUID,
			envbuild.StatusFailed,
			fmt.Sprintf("error when building env: %s", buildErr),
		)
		if err != nil {
			telemetry.ReportCriticalError(ctx, "error when setting build status", err)
		}

		return
	}

	// status building must be set after build is triggered because then
	// it's possible build status job will be triggered before build cache on template manager is created and build will fail
	err = a.templateManager.SetStatus(
		ctx,
		templateID,
		buildUUID,
		envbuild.StatusBuilding,
		"starting build",
	)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when setting build status", err)

		return
	}

	telemetry.ReportEvent(ctx, "created new environment", attribute.String("env.id", templateID))

	// Do not wait for global build sync trigger it immediately
	go func() {
		buildContext, buildSpan := a.Tracer.Start(
			trace.ContextWithSpanContext(context.Background(), span.SpanContext()),
			"template-background-build-env",
		)
		defer buildSpan.End()

		a.templateManager.BuildStatusSync(buildContext, buildUUID, templateID)

		// Invalidate the cache
		a.templateCache.Invalidate(templateID)
	}()

	a.posthog.CreateAnalyticsUserEvent(userID.String(), team.ID.String(), "built environment", posthog.NewProperties().
		Set("user_id", userID).
		Set("environment", templateID).
		Set("build_id", buildID).
		Set("duration", time.Since(startTime).String()).
		Set("success", err != nil),
	)

	c.Status(http.StatusAccepted)
}

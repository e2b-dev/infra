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
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// PostTemplatesTemplateIDBuildsBuildID triggers a new build after the user pushes the Docker image to the registry
func (a *APIStore) PostTemplatesTemplateIDBuildsBuildID(c *gin.Context, templateID api.TemplateID, buildID api.BuildID) {
	ctx := c.Request.Context()
	span := trace.SpanFromContext(ctx)

	buildUUID, err := uuid.Parse(buildID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid build ID: %s", buildID))

		err = fmt.Errorf("invalid build ID: %w", err)
		telemetry.ReportCriticalError(ctx, err)

		return
	}

	userID, teams, err := a.GetUserAndTeams(c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting default team: %s", err))

		err = fmt.Errorf("error when getting default team: %w", err)
		telemetry.ReportCriticalError(ctx, err)

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

		err = fmt.Errorf("error when getting env: %w", err)
		telemetry.ReportCriticalError(ctx, err)

		return
	}

	var team *models.Team
	// Check if the user has access to the template
	for _, t := range teams {
		if t.ID == envDB.TeamID {
			team = t
			break
		}
	}

	if team == nil {
		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("User does not have access to the template"))

		err = fmt.Errorf("user '%s' does not have access to the template '%s'", userID, templateID)
		telemetry.ReportCriticalError(ctx, err)

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
		Count(ctx)
	if err != nil {
		zap.L().Error("Error when getting count of running builds", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error during template build request")
		return
	}

	// make sure there is no other build in progress for the same template
	if concurrentlyRunningBuilds > 0 {
		a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("There is already a build in progress for the template"))
		err = fmt.Errorf("there is already a build in progress for the template '%s'", templateID)
		telemetry.ReportCriticalError(ctx, err)
		return
	}

	startTime := time.Now()
	build := envDB.Edges.Builds[0]
	startCmd := ""
	if build.StartCmd != nil {
		startCmd = *build.StartCmd
	}

	// only waiting builds can be triggered
	if build.Status != envbuild.StatusWaiting {
		a.sendAPIStoreError(c, http.StatusBadRequest, "build is not in waiting state")
		err = fmt.Errorf("build is not in waiting state: %s", build.Status)
		telemetry.ReportCriticalError(ctx, err)
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
	)

	if buildErr != nil {
		buildErr = fmt.Errorf("error when building env: %w", buildErr)
		zap.L().Error("build failed", zap.Error(buildErr))
		telemetry.ReportCriticalError(ctx, buildErr)

		err = a.templateManager.SetStatus(
			ctx,
			templateID,
			buildUUID,
			envbuild.StatusFailed,
			fmt.Sprintf("error when building env: %s", buildErr),
		)
		if err != nil {
			telemetry.ReportCriticalError(ctx, fmt.Errorf("error when setting build status: %w", err))
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
		telemetry.ReportCriticalError(ctx, fmt.Errorf("error when setting build status: %w", err))

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

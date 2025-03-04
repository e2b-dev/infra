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

	// Create a new build cache for storing logs
	err = a.buildCache.Create(templateID, buildUUID, team.ID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("there's already running build for %s", templateID))

		err = fmt.Errorf("build is already running build for %s", templateID)
		telemetry.ReportCriticalError(ctx, err)

		return
	}

	// Set the build status to building
	err = a.db.EnvBuildSetStatus(ctx, envDB.ID, buildUUID, envbuild.StatusBuilding)
	if err != nil {
		err = fmt.Errorf("error when setting build status: %w", err)
		telemetry.ReportCriticalError(ctx, err)

		a.buildCache.Delete(templateID, buildUUID, team.ID)

		return
	}

	// Trigger the build in the background
	go func() {
		buildContext, childSpan := a.Tracer.Start(
			trace.ContextWithSpanContext(context.Background(), span.SpanContext()),
			"background-build-env",
		)
		defer childSpan.End()

		startTime := time.Now()
		build := envDB.Edges.Builds[0]
		startCmd := ""
		if build.StartCmd != nil {
			startCmd = *build.StartCmd
		}

		// Call the Template Manager to build the environment
		buildErr := a.templateManager.CreateTemplate(
			a.Tracer,
			buildContext,
			a.db,
			a.buildCache,
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
			telemetry.ReportCriticalError(buildContext, buildErr)

			dbErr := a.db.EnvBuildSetStatus(buildContext, templateID, buildUUID, envbuild.StatusFailed)
			if dbErr != nil {
				telemetry.ReportCriticalError(buildContext, fmt.Errorf("error when setting build status: %w", dbErr))
			}

			// Save the error in the logs
			buildCacheErr := a.buildCache.Append(templateID, buildUUID, fmt.Sprintf("Build failed: %s\n", buildErr))
			if buildCacheErr != nil {
				telemetry.ReportCriticalError(buildContext, fmt.Errorf("error when appending build logs: %w", buildCacheErr))
			}

			cacheErr := a.buildCache.SetDone(templateID, buildUUID, api.TemplateBuildStatusError)
			if cacheErr != nil {
				telemetry.ReportCriticalError(buildContext, fmt.Errorf("error when setting build done in logs: %w", cacheErr))
			}

			return
		}

		// Invalidate the cache
		a.templateCache.Invalidate(templateID)

		a.posthog.CreateAnalyticsUserEvent(userID.String(), team.ID.String(), "built environment", posthog.NewProperties().
			Set("user_id", userID).
			Set("environment", templateID).
			Set("build_id", buildID).
			Set("duration", time.Since(startTime).String()).
			Set("success", err != nil),
		)
	}()

	c.Status(http.StatusAccepted)
}

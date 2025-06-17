package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/posthog/posthog-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	apiutils "github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envalias"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// PostV2Templates triggers a new template build
func (a *APIStore) PostV2Templates(c *gin.Context) {
	ctx := c.Request.Context()
	span := trace.SpanFromContext(ctx)

	body, err := apiutils.ParseBody[api.TemplateBuildRequestV2](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err))
		telemetry.ReportCriticalError(ctx, "invalid request body", err)

		return
	}

	telemetry.ReportEvent(ctx, "started environment build")

	// Prepare info for rebuilding env
	userID, teams, err := a.GetUserAndTeams(c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting user: %s", err))

		telemetry.ReportCriticalError(ctx, "error when getting user", err)

		return
	}

	// Find the team and tier
	team, tier, err := findTeamAndTier(teams, &body.TeamID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusNotFound, err.Error())
		telemetry.ReportCriticalError(ctx, "error finding team and tier", err)
		return
	}

	// Create the build, find the template ID by alias or generate a new one
	templateID := id.Generate()
	isNew := true
	templateAlias, err := a.db.Client.EnvAlias.Query().Where(envalias.ID(body.Alias)).Only(ctx)
	if err != nil {
		var notFoundErr *db.ErrNotFound
		if !errors.As(err, notFoundErr) {
			a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting template alias: %s", err))
			telemetry.ReportCriticalError(ctx, "error when getting template alias", err)
			return
		}
	} else {
		templateID = templateAlias.EnvID
		isNew = false
	}

	stepsMarshal, err := json.Marshal(body.Steps)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Failed serializing template steps: %s", err))
		telemetry.ReportCriticalError(ctx, "failed serializing template steps", err)
		return
	}
	buildReq := BuildTemplateRequest{
		IsNew:      isNew,
		TemplateID: templateID,
		UserID:     *userID,
		Team:       team,
		Tier:       tier,
		Dockerfile: string(stepsMarshal),
		Alias:      &body.Alias,
		StartCmd:   body.StartCmd,
		ReadyCmd:   body.ReadyCmd,
		CpuCount:   body.CpuCount,
		MemoryMB:   body.MemoryMB,
	}

	template, apiError := a.BuildTemplate(ctx, buildReq)
	if apiError != nil {
		a.sendAPIStoreError(c, apiError.Code, apiError.ClientMsg)
		telemetry.ReportCriticalError(ctx, "invalid request body", err)
		return
	}

	telemetry.ReportEvent(ctx, "started creating new environment")

	telemetry.SetAttributes(ctx,
		attribute.String("user.id", userID.String()),
		//telemetry.WithTeamID(team.ID.String()),
		telemetry.WithTemplateID(templateID),
	)

	buildID, err := uuid.Parse(template.BuildID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid build ID")
		telemetry.ReportCriticalError(ctx, "invalid build ID", err, telemetry.WithTemplateID(templateID))
		return
	}

	// Check and cancel concurrent builds
	if err := a.CheckAndCancelConcurrentBuilds(ctx, templateID, buildID); err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error during template build request")
		return
	}

	startTime := time.Now()

	// Call the Template Manager to build the environment
	buildErr := a.templateManager.CreateTemplate(
		a.Tracer,
		ctx,
		templateID,
		buildID,
		template.KernelVersion,
		template.FirecrackerVersion,
		template.StartCmd,
		template.VCpu,
		template.FreeDiskSizeMB,
		template.MemoryMB,
		template.ReadyCmd,
		body.FromImage,
		body.Steps,
	)

	if buildErr != nil {
		telemetry.ReportCriticalError(ctx, "build failed", buildErr, telemetry.WithTemplateID(templateID))

		err = a.templateManager.SetStatus(
			ctx,
			templateID,
			buildID,
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
		buildID,
		envbuild.StatusBuilding,
		"starting build",
	)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when setting build status")
		telemetry.ReportCriticalError(ctx, "error when setting build status", err)
		return
	}

	telemetry.ReportEvent(ctx, "created new environment", telemetry.WithTemplateID(templateID))

	// Do not wait for global build sync trigger it immediately
	go func() {
		buildContext, buildSpan := a.Tracer.Start(
			trace.ContextWithSpanContext(context.Background(), span.SpanContext()),
			"template-background-build-env",
		)
		defer buildSpan.End()

		a.templateManager.BuildStatusSync(buildContext, buildID, templateID)

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

	c.JSON(http.StatusAccepted, &api.Template{
		TemplateID: template.TemplateID,
		BuildID:    template.BuildID,
		Public:     template.Public,
		Aliases:    template.Aliases,
	})
}

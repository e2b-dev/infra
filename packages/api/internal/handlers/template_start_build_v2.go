package handlers

import (
	"context"
	"encoding/json"
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
	apiutils "github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type dockerfileStore struct {
	FromImage string              `json:"from_image"`
	Steps     *[]api.TemplateStep `json:"steps"`
}

// PostV2TemplatesTemplateIDBuildsBuildID triggers a new build
func (a *APIStore) PostV2TemplatesTemplateIDBuildsBuildID(c *gin.Context, templateID api.TemplateID, buildID api.BuildID) {
	ctx := c.Request.Context()
	span := trace.SpanFromContext(ctx)

	body, err := apiutils.ParseBody[api.TemplateBuildStartV2](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err))
		telemetry.ReportCriticalError(ctx, "invalid request body", err)

		return
	}

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

		telemetry.ReportCriticalError(ctx, "error when getting env", err, telemetry.WithTemplateID(templateID))

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
	build := envDB.Edges.Builds[0]

	// only waiting builds can be triggered
	if build.Status != envbuild.StatusWaiting {
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

	stepsMarshalled, err := json.Marshal(dockerfileStore{
		FromImage: body.FromImage,
		Steps:     body.Steps,
	})
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when processing steps: %s", err))
		telemetry.ReportCriticalError(ctx, "error when processing steps", err, telemetry.WithTemplateID(templateID))
		return
	}

	err = a.db.Client.EnvBuild.Update().
		SetNillableStartCmd(body.StartCmd).
		SetNillableReadyCmd(body.ReadyCmd).
		SetDockerfile(string(stepsMarshalled)).
		Where(envbuild.ID(buildUUID)).
		Exec(ctx)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when updating build", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when updating build: %s", err))
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
		body.StartCmd,
		build.Vcpu,
		build.FreeDiskSizeMB,
		build.RAMMB,
		body.ReadyCmd,
		body.FromImage,
		body.Force,
		body.Steps,
		team.ClusterID,
		build.ClusterNodeID,
	)

	if buildErr != nil {
		telemetry.ReportCriticalError(ctx, "build failed", buildErr, telemetry.WithTemplateID(templateID))

		msg := fmt.Sprintf("error when building env: %s", buildErr)
		err = a.templateManager.SetStatus(
			ctx,
			templateID,
			buildUUID,
			envbuild.StatusFailed,
			&msg,
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
		nil,
	)
	if err != nil {
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

		err := a.templateManager.BuildStatusSync(buildContext, buildUUID, templateID, team.ClusterID, build.ClusterNodeID)
		if err != nil {
			zap.L().Error("error syncing build status", zap.Error(err))
		}

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

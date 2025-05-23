package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/db/queries"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) startSandbox(
	ctx context.Context,
	sandboxID string,
	timeout time.Duration,
	envVars map[string]string,
	metadata map[string]string,
	alias string,
	team authcache.AuthTeamInfo,
	build queries.EnvBuild,
	requestHeader *http.Header,
	isResume bool,
	clientID *string,
	baseTemplateID string,
	autoPause bool,
	envdAccessToken *string,
) (*api.Sandbox, *api.APIError) {
	startTime := time.Now()
	endTime := startTime.Add(timeout)

	// Unique ID for the execution (from start/resume to stop/pause)
	executionID := uuid.New().String()
	sandbox, instanceErr := a.orchestrator.CreateSandbox(
		ctx,
		sandboxID,
		executionID,
		alias,
		team,
		build,
		metadata,
		envVars,
		startTime,
		endTime,
		timeout,
		isResume,
		clientID,
		baseTemplateID,
		autoPause,
		envdAccessToken,
	)
	if instanceErr != nil {
		errMsg := fmt.Errorf("error when creating instance: %w", instanceErr.Err)
		telemetry.ReportCriticalError(ctx, errMsg)
		return nil, instanceErr
	}

	telemetry.ReportEvent(ctx, "Created sandbox")

	_, analyticsSpan := a.Tracer.Start(ctx, "analytics")
	a.posthog.IdentifyAnalyticsTeam(team.Team.ID.String(), team.Team.Name)
	properties := a.posthog.GetPackageToPosthogProperties(requestHeader)
	a.posthog.CreateAnalyticsTeamEvent(team.Team.ID.String(), "created_instance",
		properties.
			Set("environment", *build.EnvID).
			Set("instance_id", sandbox.SandboxID).
			Set("alias", alias),
	)
	analyticsSpan.End()

	telemetry.ReportEvent(ctx, "Created analytics event")

	go func() {
		a.templateSpawnCounter.IncreaseTemplateSpawnCount(baseTemplateID, time.Now())
	}()

	telemetry.SetAttributes(ctx,
		attribute.String("instance.id", sandbox.SandboxID),
	)

	sbxlogger.E(&sbxlogger.SandboxMetadata{
		SandboxID:  sandbox.SandboxID,
		TemplateID: *build.EnvID,
		TeamID:     team.Team.ID.String(),
	}).Info("Sandbox created", zap.String("end_time", endTime.Format("2006-01-02 15:04:05 -07:00")))

	return &api.Sandbox{
		ClientID:        sandbox.ClientID,
		SandboxID:       sandbox.SandboxID,
		TemplateID:      *build.EnvID,
		Alias:           &alias,
		EnvdVersion:     *build.EnvdVersion,
		EnvdAccessToken: envdAccessToken,
	}, nil
}

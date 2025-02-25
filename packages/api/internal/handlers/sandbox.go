package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) startSandbox(
	ctx context.Context,
	sandboxID string,
	timeout time.Duration,
	envVars,
	metadata map[string]string,
	alias string,
	team authcache.AuthTeamInfo,
	build *models.EnvBuild,
	logger *logs.SandboxLogger,
	requestHeader *http.Header,
	isResume bool,
	clientID *string,
	baseTemplateID string,
	autoPause bool,
) (*api.Sandbox, error) {
	startTime := time.Now()
	endTime := startTime.Add(timeout)

	sandbox, instanceErr := a.orchestrator.CreateSandbox(
		ctx,
		sandboxID,
		alias,
		team,
		build,
		metadata,
		envVars,
		startTime,
		endTime,
		timeout,
		logger,
		isResume,
		clientID,
		baseTemplateID,
		autoPause,
	)
	if instanceErr != nil {
		errMsg := fmt.Errorf("error when creating instance: %w", instanceErr)
		telemetry.ReportCriticalError(ctx, errMsg)

		return nil, errMsg
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

	logger.Infof("Sandbox created with - end time: %s", endTime.Format("2006-01-02 15:04:05 -07:00"))

	return &api.Sandbox{
		ClientID:    sandbox.ClientID,
		SandboxID:   sandbox.SandboxID,
		TemplateID:  *build.EnvID,
		Alias:       &alias,
		EnvdVersion: *build.EnvdVersion,
	}, nil
}

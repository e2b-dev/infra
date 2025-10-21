package handlers

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/handlers")

func (a *APIStore) startSandbox(
	ctx context.Context,
	sandboxID string,
	timeout time.Duration,
	envVars map[string]string,
	metadata map[string]string,
	alias string,
	team *types.Team,
	build queries.EnvBuild,
	requestHeader *http.Header,
	isResume bool,
	nodeID *string,
	baseTemplateID string,
	autoPause bool,
	envdAccessToken *string,
	allowInternetAccess *bool,
	mcp api.Mcp,
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
		nodeID,
		baseTemplateID,
		autoPause,
		envdAccessToken,
		allowInternetAccess,
	)
	if instanceErr != nil {
		telemetry.ReportError(ctx, "error when creating instance", instanceErr.Err)

		return nil, instanceErr
	}

	telemetry.ReportEvent(ctx, "Created sandbox")

	_, analyticsSpan := tracer.Start(ctx, "analytics")
	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	properties := a.posthog.GetPackageToPosthogProperties(requestHeader)
	props := properties.
		Set("environment", build.EnvID).
		Set("instance_id", sandbox.SandboxID).
		Set("alias", alias)

	if mcp != nil {
		props = props.Set("mcp_servers", slices.Collect(maps.Keys(mcp)))
	}

	a.posthog.CreateAnalyticsTeamEvent(team.ID.String(), "created_instance", props)
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
		TemplateID: build.EnvID,
		TeamID:     team.ID.String(),
	}).Info("Sandbox created", zap.String("end_time", endTime.Format("2006-01-02 15:04:05 -07:00")))

	return sandbox.ToAPISandbox(), nil
}

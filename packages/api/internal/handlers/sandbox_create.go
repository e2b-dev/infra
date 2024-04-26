package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/semaphore"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const defaultRequestLimit = 16
const InstanceIDPrefix = "i"

var postSandboxParallelLimit = semaphore.NewWeighted(defaultRequestLimit)

func (a *APIStore) PostSandboxes(c *gin.Context) {
	ctx := c.Request.Context()

	telemetry.ReportEvent(ctx, "Parsed body")

	body, err := utils.ParseBody[api.PostSandboxesJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		errMsg := fmt.Errorf("error when parsing request: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	span := trace.SpanFromContext(ctx)
	c.Set("traceID", span.SpanContext().TraceID().String())

	cleanedAliasOrEnvID, err := utils.CleanEnvID(body.TemplateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid environment ID: %s", err))

		errMsg := fmt.Errorf("error when cleaning env ID: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	telemetry.ReportEvent(ctx, "Cleaned sandbox ID")

	// Get team from context, use TeamContextKey
	team := c.Value(auth.TeamContextKey).(models.Team)

	// Check if team has access to the environment
	env, build, checkErr := a.CheckTeamAccessEnv(ctx, cleanedAliasOrEnvID, team.ID, true)
	if checkErr != nil {
		errMsg := fmt.Errorf("error when checking team access: %w", checkErr)
		telemetry.ReportCriticalError(ctx, errMsg)

		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when checking team access: %s", checkErr))

		return
	}

	telemetry.ReportEvent(ctx, "Checked team access")

	var alias string
	if env.Aliases != nil && len(*env.Aliases) > 0 {
		alias = (*env.Aliases)[0]
	}

	c.Set("envID", env.TemplateID)
	c.Set("teamID", team.ID.String())

	telemetry.SetAttributes(ctx,
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.id", env.TemplateID),
		attribute.String("env.alias", alias),
		attribute.String("env.kernel.version", build.KernelVersion),
		attribute.String("env.firecracker.version", build.FirecrackerVersion),
	)

	telemetry.ReportEvent(ctx, "waiting for create sandbox parallel limit semaphore slot")

	limitErr := postSandboxParallelLimit.Acquire(ctx, 1)
	if limitErr != nil {
		errMsg := fmt.Errorf("error when acquiring parallel lock: %w", limitErr)
		telemetry.ReportCriticalError(ctx, errMsg)

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Request canceled or timed out.")

		return
	}

	defer postSandboxParallelLimit.Release(1)
	telemetry.ReportEvent(ctx, "create sandbox parallel limit semaphore slot acquired")

	sandboxID := InstanceIDPrefix + utils.GenerateID()

	var metadata map[string]string
	if body.Metadata != nil {
		metadata = *body.Metadata
	}

	sandbox, instanceErr := a.orchestrator.CreateSandbox(
		a.tracer,
		ctx,
		sandboxID,
		env.TemplateID,
		alias,
		team.ID,
		build.ID,
		team.Edges.TeamTier.MaxLengthHours,
		team.Edges.TeamTier.ConcurrentInstances,
		metadata,
		build.KernelVersion,
		build.FirecrackerVersion,
		build.Vcpu,
		build.RAMMB,
	)
	if instanceErr != nil {
		telemetry.ReportCriticalError(ctx, instanceErr.Err)
		a.sendAPIStoreError(c, instanceErr.Code, instanceErr.ClientMsg)

		return
	}

	telemetry.ReportEvent(ctx, "Created sandbox")
	c.Set("instanceID", sandbox.SandboxID)

	telemetry.ReportEvent(ctx, "Added sandbox to cache")

	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.CreateAnalyticsTeamEvent(team.ID.String(), "created_instance",
		properties.
			Set("environment", env.TemplateID).
			Set("instance_id", sandbox.SandboxID).
			Set("alias", alias),
	)

	telemetry.ReportEvent(ctx, "Created analytics event")

	go func() {
		err = a.db.UpdateEnvLastUsed(context.Background(), env.TemplateID)
		if err != nil {
			a.logger.Errorf("Error when updating last used for env: %s", err)
		}
	}()

	telemetry.SetAttributes(ctx,
		attribute.String("instance.id", sandbox.SandboxID),
	)

	a.logger.Infof("Created sandbox '%s' for team '%s'", sandbox.SandboxID, team.ID)

	c.JSON(http.StatusCreated, &sandbox)
}

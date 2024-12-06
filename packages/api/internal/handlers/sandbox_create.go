package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/semaphore"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	defaultRequestLimit = 16
	InstanceIDPrefix    = "i"
)

var postSandboxParallelLimit = semaphore.NewWeighted(defaultRequestLimit)

func (a *APIStore) PostSandboxes(c *gin.Context) {
	ctx := c.Request.Context()
	sandboxID := InstanceIDPrefix + id.Generate()

	// Get team from context, use TeamContextKey
	teamInfo := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)
	team := teamInfo.Team

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	telemetry.ReportEvent(ctx, "Parsed body")

	body, err := utils.ParseBody[api.PostSandboxesJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		errMsg := fmt.Errorf("error when parsing request: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	cleanedAliasOrEnvID, err := id.CleanEnvID(body.TemplateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid environment ID: %s", err))

		errMsg := fmt.Errorf("error when cleaning env ID: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	telemetry.ReportEvent(ctx, "Cleaned sandbox ID")

	_, templateSpan := a.Tracer.Start(ctx, "get-template")
	// Check if team has access to the environment
	env, build, checkErr := a.templateCache.Get(ctx, cleanedAliasOrEnvID, team.ID, true)
	if checkErr != nil {
		errMsg := fmt.Errorf("error when checking team access: %s", checkErr.Err)
		telemetry.ReportCriticalError(ctx, errMsg)

		a.sendAPIStoreError(c, checkErr.Code, fmt.Sprintf("Error when checking team access: %s", checkErr.ClientMsg))

		return
	}
	templateSpan.End()

	sandboxLogger := logs.NewSandboxLogger(sandboxID, env.TemplateID, team.ID.String(), int32(build.Vcpu), int32(build.RAMMB), false)
	sandboxLogger.Debugf("Started creating sandbox")
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

	_, rateSpan := a.Tracer.Start(ctx, "rate-limit")
	counter, err := meters.GetUpDownCounter(meters.RateLimitCounterMeterName)
	if err != nil {
		a.logger.Errorf("error getting counter: %s", err)
	}

	counter.Add(ctx, 1)
	limitErr := postSandboxParallelLimit.Acquire(ctx, 1)
	counter.Add(ctx, -1)
	if limitErr != nil {
		errMsg := fmt.Errorf("error when acquiring parallel lock: %w", limitErr)
		telemetry.ReportCriticalError(ctx, errMsg)

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Request canceled or timed out.")

		return
	}

	defer postSandboxParallelLimit.Release(1)
	telemetry.ReportEvent(ctx, "create sandbox parallel limit semaphore slot acquired")

	// Check if team has reached max instances
	maxInstancesPerTeam := teamInfo.Tier.ConcurrentInstances
	err, releaseTeamSandboxReservation := a.instanceCache.Reserve(sandboxID, team.ID, maxInstancesPerTeam)
	if err != nil {
		errMsg := fmt.Errorf("team '%s' has reached the maximum number of instances (%d)", team.ID, teamInfo.Tier.ConcurrentInstances)
		telemetry.ReportCriticalError(ctx, fmt.Errorf("%w (error: %w)", errMsg, err))

		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf(
			"You have reached the maximum number of concurrent E2B sandboxes (%d). If you need more, "+
				"please contact us at 'https://e2b.dev/docs/getting-help'", maxInstancesPerTeam))

		return
	}

	rateSpan.End()
	telemetry.ReportEvent(ctx, "Reserved team sandbox slot")

	defer releaseTeamSandboxReservation()

	var metadata map[string]string
	if body.Metadata != nil {
		metadata = *body.Metadata
	}

	var envVars map[string]string
	if body.EnvVars != nil {
		envVars = *body.EnvVars
	}

	startTime := time.Now()
	timeout := instance.InstanceExpiration
	if body.Timeout != nil {
		timeout = time.Duration(*body.Timeout) * time.Second

		if timeout > time.Duration(teamInfo.Tier.MaxLengthHours)*time.Hour {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Timeout cannot be greater than %d hours", teamInfo.Tier.MaxLengthHours))

			return
		}
	}

	endTime := startTime.Add(timeout)

	sandbox, instanceErr := a.orchestrator.CreateSandbox(a.Tracer, ctx, sandboxID, env.TemplateID, alias, team.ID.String(), build, teamInfo.Tier.MaxLengthHours, metadata, envVars, build.KernelVersion, build.FirecrackerVersion, *build.EnvdVersion, startTime, endTime)
	if instanceErr != nil {
		errMsg := fmt.Errorf("error when creating instance: %w", instanceErr)
		telemetry.ReportCriticalError(ctx, errMsg)

		apiErr := api.Error{
			Code:    http.StatusInternalServerError,
			Message: errMsg.Error(),
		}

		a.sendAPIStoreError(c, int(apiErr.Code), apiErr.Message)

		return
	}

	telemetry.ReportEvent(ctx, "Created sandbox")

	// This is to compensate for the time it takes to start the instance
	// Otherwise it could cause the instance to expire before user has a chance to use it
	startTime = time.Now()
	endTime = startTime.Add(timeout)
	instanceInfo := instance.InstanceInfo{
		Logger:            sandboxLogger,
		StartTime:         startTime,
		EndTime:           endTime,
		Instance:          sandbox,
		BuildID:           &build.ID,
		TeamID:            &team.ID,
		Metadata:          metadata,
		MaxInstanceLength: time.Duration(teamInfo.Tier.MaxLengthHours) * time.Hour,
	}

	_, cacheSpan := a.Tracer.Start(ctx, "add-instance-to-cache")
	if cacheErr := a.instanceCache.Add(instanceInfo, true); cacheErr != nil {
		errMsg := fmt.Errorf("error when adding instance to cache: %w", cacheErr)
		telemetry.ReportError(ctx, errMsg)

		delErr := a.DeleteInstance(sandbox.SandboxID, true)
		if delErr != nil {
			delErrMsg := fmt.Errorf("couldn't delete instance that couldn't be added to cache: %w", delErr.Err)
			telemetry.ReportError(ctx, delErrMsg)
		} else {
			telemetry.ReportEvent(ctx, "deleted instance that couldn't be added to cache")
		}

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Cannot create a sandbox right now")

		return
	}

	cacheSpan.End()

	c.Set("instanceID", sandbox.SandboxID)

	telemetry.ReportEvent(ctx, "Added sandbox to cache")

	_, analyticsSpan := a.Tracer.Start(ctx, "analytics")
	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.CreateAnalyticsTeamEvent(team.ID.String(), "created_instance",
		properties.
			Set("environment", env.TemplateID).
			Set("instance_id", sandbox.SandboxID).
			Set("alias", alias),
	)
	analyticsSpan.End()

	telemetry.ReportEvent(ctx, "Created analytics event")

	go func() {
		a.batcher.UpdateTemplateSpawnCount(env.TemplateID)
	}()

	telemetry.SetAttributes(ctx,
		attribute.String("instance.id", sandbox.SandboxID),
	)

	sandboxLogger.Infof("Sandbox created with - end time: %s", endTime.Format("2006-01-02 15:04:05 -07:00"))

	c.JSON(http.StatusCreated, &sandbox)
}

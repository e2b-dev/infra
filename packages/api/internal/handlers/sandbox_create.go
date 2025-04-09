package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/middleware/otel/metrics"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	InstanceIDPrefix   = "i"
	metricTemplateName = metrics.MetricPrefix + "template.name"
)

var (
	// mostUsedTemplates is a map of the most used template IDs to their alias names.
	// It is used to reduce metric cardinality.
	mostUsedTemplates = map[string]string{
		"rki5dems9wqfm4r03t7g": "base",
		"nlhz8vlwyupq845jsdg9": "code-interpreter-v1",
		"3e4rngfa34txe0gxc1zf": "code-interpreter-beta",
		"k0wmnzir0zuzye6dndlw": "desktop-dev1",
	}
)

func (a *APIStore) PostSandboxes(c *gin.Context) {
	ctx := c.Request.Context()

	// Get team from context, use TeamContextKey
	teamInfo := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)

	c.Set("teamID", teamInfo.Team.ID.String())

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	body, err := utils.ParseBody[api.PostSandboxesJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		errMsg := fmt.Errorf("error when parsing request: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	telemetry.ReportEvent(ctx, "Parsed body")

	cleanedAliasOrEnvID, err := id.CleanEnvID(body.TemplateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid environment ID: %s", err))

		errMsg := fmt.Errorf("error when cleaning env ID: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	telemetry.ReportEvent(ctx, "Cleaned template ID")

	_, templateSpan := a.Tracer.Start(ctx, "get-template")
	defer templateSpan.End()

	// Check if team has access to the environment
	env, build, checkErr := a.templateCache.Get(ctx, cleanedAliasOrEnvID, teamInfo.Team.ID, true)
	if checkErr != nil {
		telemetry.ReportCriticalError(ctx, checkErr.Err)

		a.sendAPIStoreError(c, checkErr.Code, checkErr.ClientMsg)
		return
	}
	templateSpan.End()

	telemetry.ReportEvent(ctx, "Checked team access")

	c.Set("envID", env.TemplateID)
	setTemplateNameMetric(c, env.TemplateID)

	sandboxID := InstanceIDPrefix + id.Generate()

	c.Set("instanceID", sandboxID)

	sbxlogger.E(&sbxlogger.SandboxMetadata{
		SandboxID:  sandboxID,
		TemplateID: env.TemplateID,
		TeamID:     teamInfo.Team.ID.String(),
	}).Debug("Started creating sandbox")

	var alias string
	if env.Aliases != nil && len(*env.Aliases) > 0 {
		alias = (*env.Aliases)[0]
	}

	telemetry.SetAttributes(ctx,
		attribute.String("env.team.id", teamInfo.Team.ID.String()),
		attribute.String("env.id", env.TemplateID),
		attribute.String("env.alias", alias),
		attribute.String("env.kernel.version", build.KernelVersion),
		attribute.String("env.firecracker.version", build.FirecrackerVersion),
	)

	var metadata map[string]string
	if body.Metadata != nil {
		metadata = *body.Metadata
	}

	var envVars map[string]string
	if body.EnvVars != nil {
		envVars = *body.EnvVars
	}

	timeout := instance.InstanceExpiration
	if body.Timeout != nil {
		timeout = time.Duration(*body.Timeout) * time.Second

		if timeout > time.Duration(teamInfo.Tier.MaxLengthHours)*time.Hour {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Timeout cannot be greater than %d hours", teamInfo.Tier.MaxLengthHours))

			return
		}
	}

	autoPause := instance.InstanceAutoPauseDefault
	if body.AutoPause != nil {
		autoPause = *body.AutoPause
	}

	sandbox, createErr := a.startSandbox(
		ctx,
		sandboxID,
		timeout,
		envVars,
		metadata,
		alias,
		teamInfo,
		build,
		&c.Request.Header,
		false,
		nil,
		env.TemplateID,
		autoPause,
	)
	if createErr != nil {
		zap.L().Error("Failed to create sandbox", zap.Error(createErr.Err))
		a.sendAPIStoreError(c, createErr.Code, createErr.ClientMsg)

		return
	}

	c.Set("nodeID", sandbox.ClientID)

	c.JSON(http.StatusCreated, &sandbox)
}

func setTemplateNameMetric(c *gin.Context, templateID string) {
	v, ok := mostUsedTemplates[templateID]
	if !ok {
		// template ID is not most used, set the name to "other"
		v = "other"
	}

	c.Set(metricTemplateName, v)
}

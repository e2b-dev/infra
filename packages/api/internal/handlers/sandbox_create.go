package handlers

import (
	"errors"
	"fmt"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"golang.org/x/mod/semver"
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
	InstanceIDPrefix    = "i"
	metricTemplateAlias = metrics.MetricPrefix + "template.alias"
)

var (
	// mostUsedTemplates is a map of the most used template aliases.
	// It is used for monitoring and to reduce metric cardinality.
	mostUsedTemplates = map[string]struct{}{
		"base":                  {},
		"code-interpreter-v1":   {},
		"code-interpreter-beta": {},
		"desktop":               {},
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
	if aliases := env.Aliases; aliases != nil {
		setTemplateNameMetric(c, *aliases)
	}

	sandboxID := InstanceIDPrefix + id.Generate()

	c.Set("instanceID", sandboxID)

	sbxlogger.E(&sbxlogger.SandboxMetadata{
		SandboxID:  sandboxID,
		TemplateID: env.TemplateID,
		TeamID:     teamInfo.Team.ID.String(),
	}).Debug("Started creating sandbox")

	alias := firstAlias(env.Aliases)
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

	var envdAccessToken *string = nil
	if body.Secure != nil && *body.Secure == true {
		accessToken, tokenErr := a.getEnvdAccessToken(build.EnvdVersion, sandboxID)
		if tokenErr != nil {
			zap.L().Error("Secure envd access token error", zap.Error(tokenErr.Err), zap.String("sandboxID", sandboxID), zap.String("buildID", build.ID.String()))
			a.sendAPIStoreError(c, tokenErr.Code, tokenErr.ClientMsg)
			return
		}

		envdAccessToken = &accessToken
	}

	sbx, createErr := a.startSandbox(
		ctx,
		sandboxID,
		timeout,
		envVars,
		metadata,
		alias,
		teamInfo,
		*build,
		&c.Request.Header,
		false,
		nil,
		env.TemplateID,
		autoPause,
		envdAccessToken,
	)
	if createErr != nil {
		zap.L().Error("Failed to create sandbox", zap.Error(createErr.Err))
		a.sendAPIStoreError(c, createErr.Code, createErr.ClientMsg)
		return
	}

	c.Set("nodeID", sbx.ClientID)

	c.JSON(http.StatusCreated, &sbx)
}

func (a *APIStore) getEnvdAccessToken(envdVersion *string, sandboxID string) (string, *api.APIError) {
	if envdVersion == nil {
		return "", &api.APIError{
			Code:      http.StatusBadRequest,
			ClientMsg: "you need to re-build template to allow secure flag",
			Err:       errors.New("envd version is required during envd access token creation"),
		}
	}

	// check if the envd version is newer than 0.2.0
	if semver.Compare(fmt.Sprintf("v%s", *envdVersion), "v0.2.0") < 0 {
		return "", &api.APIError{
			Code:      http.StatusBadRequest,
			ClientMsg: "current template build does not support access flag, you need to re-build template to allow it",
			Err:       errors.New("envd version is not supported for secure flag"),
		}
	}

	hashed := sandbox.NewEnvdAccessTokenGenerator()
	key, err := hashed.GenerateAccessToken(sandboxID)
	if err != nil {
		return "", &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "unknown error during sandbox access token generation",
			Err:       err,
		}
	}

	return key, nil

}

func setTemplateNameMetric(c *gin.Context, aliases []string) {
	for _, alias := range aliases {
		if _, exists := mostUsedTemplates[alias]; exists {
			c.Set(metricTemplateAlias, alias)
			return
		}
	}

	// Fallback to 'other' if no match of mostUsedTemplates found
	c.Set(metricTemplateAlias, "other")
}

func firstAlias(aliases *[]string) string {
	if aliases == nil {
		return ""
	}
	if len(*aliases) == 0 {
		return ""
	}
	return (*aliases)[0]
}

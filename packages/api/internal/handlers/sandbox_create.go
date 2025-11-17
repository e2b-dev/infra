package handlers

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/net/idna"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	typesteam "github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/middleware/otel/metrics"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	InstanceIDPrefix            = "i"
	metricTemplateAlias         = metrics.MetricPrefix + "template.alias"
	minEnvdVersionForSecureFlag = "0.2.0" // Minimum version of envd that supports secure flag
)

// mostUsedTemplates is a map of the most used template aliases.
// It is used for monitoring and to reduce metric cardinality.
var mostUsedTemplates = map[string]struct{}{
	"base":                  {},
	"code-interpreter-v1":   {},
	"code-interpreter-beta": {},
	"desktop":               {},
}

func (a *APIStore) PostSandboxes(c *gin.Context) {
	ctx := c.Request.Context()

	// Get team from context, use TeamContextKey
	teamInfo := c.Value(auth.TeamContextKey).(*typesteam.Team)

	c.Set("teamID", teamInfo.Team.ID.String())

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	body, err := utils.ParseBody[api.PostSandboxesJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	telemetry.ReportEvent(ctx, "Parsed body")

	cleanedAliasOrEnvID, err := id.CleanTemplateID(body.TemplateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid environment ID: %s", err))

		telemetry.ReportCriticalError(ctx, "error when cleaning env ID", err)

		return
	}

	telemetry.ReportEvent(ctx, "Cleaned template ID")

	_, templateSpan := tracer.Start(ctx, "get-template")
	defer templateSpan.End()

	// Check if team has access to the environment
	clusterID := utils.WithClusterFallback(teamInfo.Team.ClusterID)
	env, build, checkErr := a.templateCache.Get(ctx, cleanedAliasOrEnvID, teamInfo.Team.ID, clusterID, true)
	if checkErr != nil {
		telemetry.ReportCriticalError(ctx, "error when getting template", checkErr.Err)
		a.sendAPIStoreError(c, checkErr.Code, checkErr.ClientMsg)

		return
	}
	templateSpan.End()

	telemetry.ReportEvent(ctx, "Checked team access")

	c.Set("envID", env.TemplateID)
	setTemplateNameMetric(c, env.Aliases)

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
		telemetry.WithTemplateID(env.TemplateID),
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

	var mcp api.Mcp
	if body.Mcp != nil {
		mcp = *body.Mcp
	}

	timeout := sandbox.SandboxTimeoutDefault
	if body.Timeout != nil {
		timeout = time.Duration(*body.Timeout) * time.Second

		if timeout > time.Duration(teamInfo.Limits.MaxLengthHours)*time.Hour {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Timeout cannot be greater than %d hours", teamInfo.Limits.MaxLengthHours))

			return
		}
	}

	autoPause := sandbox.AutoPauseDefault
	if body.AutoPause != nil {
		autoPause = *body.AutoPause
	}

	var envdAccessToken *string = nil
	if body.Secure != nil && *body.Secure == true {
		accessToken, tokenErr := a.getEnvdAccessToken(build.EnvdVersion, sandboxID)
		if tokenErr != nil {
			telemetry.ReportError(ctx, "secure envd access token error", tokenErr.Err, telemetry.WithSandboxID(sandboxID), telemetry.WithBuildID(build.ID.String()))
			a.sendAPIStoreError(c, tokenErr.Code, tokenErr.ClientMsg)

			return
		}

		envdAccessToken = &accessToken
	}

	allowInternetAccess := body.AllowInternetAccess

	var network *types.SandboxNetworkConfig
	if n := body.Network; n != nil {
		if err := validateNetworkConfig(n); err != nil {
			telemetry.ReportError(ctx, "invalid network config", err.Err, telemetry.WithSandboxID(sandboxID))
			a.sendAPIStoreError(c, err.Code, err.ClientMsg)

			return
		}

		network = &types.SandboxNetworkConfig{
			Ingress: &types.SandboxNetworkIngressConfig{
				AllowPublicAccess: n.AllowPublicTraffic,
				MaskRequestHost:   n.MaskRequestHost,
			},
			Egress: &types.SandboxNetworkEgressConfig{
				AllowedAddresses: sharedUtils.DerefOrDefault(n.AllowOut, nil),
				DeniedAddresses:  sharedUtils.DerefOrDefault(n.DenyOut, nil),
			},
		}

		// Make sure envd seucre access is enforced when public access is disabled,
		// this is requirement forcing users using newer features to secure sandboxes properly.
		if !sharedUtils.DerefOrDefault(network.Ingress.AllowPublicAccess, types.AllowPublicAccessDefault) && envdAccessToken == nil {
			a.sendAPIStoreError(c, http.StatusBadRequest, "You cannot create a sandbox without public access unless you enable secure envd access via 'secure' flag.")

			return
		}
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
		allowInternetAccess,
		network,
		mcp,
	)
	if createErr != nil {
		zap.L().Error("Failed to create sandbox", zap.Error(createErr.Err))
		a.sendAPIStoreError(c, createErr.Code, createErr.ClientMsg)

		return
	}

	c.JSON(http.StatusCreated, &sbx)
}

func (a *APIStore) getEnvdAccessToken(envdVersion *string, sandboxID string) (string, *api.APIError) {
	if envdVersion == nil {
		return "", &api.APIError{
			Code:      http.StatusBadRequest,
			ClientMsg: "You need to re-build template to allow using secured access. Please visit https://e2b.dev/docs/sandbox/secured-access for more information.",
			Err:       errors.New("envd version is required during envd access token creation"),
		}
	}

	// check if the envd version is at least 0.2.0
	ok, err := sharedUtils.IsGTEVersion(*envdVersion, minEnvdVersionForSecureFlag)
	if err != nil {
		return "", &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "error during envd version check",
			Err:       err,
		}
	}
	if !ok {
		return "", &api.APIError{
			Code:      http.StatusBadRequest,
			ClientMsg: "Template is not compatible with secured access. Please visit https://e2b.dev/docs/sandbox/secured-access for more information.",
			Err:       errors.New("envd version is not supported for secure flag"),
		}
	}

	key, err := a.accessTokenGenerator.GenerateEnvdAccessToken(sandboxID)
	if err != nil {
		return "", &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "error during sandbox access token generation",
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

func firstAlias(aliases []string) string {
	if len(aliases) == 0 {
		return ""
	}

	return aliases[0]
}

func splitHostPortOptional(hostport string) (host string, port string, err error) {
	host, port, err = net.SplitHostPort(hostport)
	if err != nil {
		if strings.Contains(err.Error(), "missing port") {
			return hostport, "", nil
		}

		return "", "", err
	}

	return host, port, nil
}

func validateNetworkConfig(network *api.SandboxNetworkConfig) *api.APIError {
	if network == nil {
		return nil
	}

	if maskRequestHost := network.MaskRequestHost; maskRequestHost != nil {
		hostname, _, err := splitHostPortOptional(*maskRequestHost)
		if err != nil {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("invalid mask request host (%s): %w", *maskRequestHost, err),
				ClientMsg: fmt.Sprintf("mask request host is not valid: %s", *maskRequestHost),
			}
		}

		host, err := idna.Display.ToASCII(hostname)
		if err != nil {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("invalid mask request host (%s): %w", *maskRequestHost, err),
				ClientMsg: fmt.Sprintf("mask request host is not valid: %s", *maskRequestHost),
			}
		}

		if !strings.EqualFold(host, hostname) {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("mask request host is not ASCII (%s)!=(%s)", host, hostname),
				ClientMsg: fmt.Sprintf("mask request host '%s' is not ASCII. Please use ASCII characters only.", hostname),
			}
		}
	}

	return nil
}

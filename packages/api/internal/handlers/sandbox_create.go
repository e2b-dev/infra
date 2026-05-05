package handlers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/asaskevich/govalidator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/net/http/httpguts"
	"golang.org/x/net/idna"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	apiorch "github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/clusters"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/middleware/otel/metrics"
	catalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	InstanceIDPrefix            = "i"
	metricTemplateAlias         = metrics.MetricPrefix + "template.alias"
	minEnvdVersionForSecureFlag = "0.2.0" // Minimum version of envd that supports secure flag

	// Network validation error messages
	ErrMsgDomainsRequireBlockAll = "When specifying allowed domains in allow out, you must include 'ALL_TRAFFIC' in deny out to block all other traffic."

	maxNetworkRuleDomains             = 10
	maxNetworkRuleTransformsPerDomain = 1
	maxNetworkRuleDomainLen           = 128
	maxNetworkRuleHeaderNameLen       = 64
	maxNetworkRuleHeaderValueLen      = 2048
	maxNetworkRuleHeadersPerRule      = 20
)

func (a *APIStore) PostSandboxes(c *gin.Context) {
	ctx := c.Request.Context()

	// Get team from context, use TeamContextKey
	teamInfo := auth.MustGetTeamInfo(c)

	c.Set("teamID", teamInfo.Team.ID.String())

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	body, err := ginutils.ParseBody[api.PostSandboxesJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	telemetry.ReportEvent(ctx, "Parsed body")

	identifier, tag, err := id.ParseName(body.TemplateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid template reference: %s", err))
		telemetry.ReportError(ctx, "invalid template reference", err)

		return
	}

	clusterID := clusters.WithClusterFallback(teamInfo.Team.ClusterID)
	aliasInfo, err := a.templateCache.ResolveAlias(ctx, identifier, teamInfo.Team.Slug)
	if err != nil {
		apiErr := templatecache.ErrorToAPIError(err, identifier)
		telemetry.ReportErrorByCode(ctx, apiErr.Code, "error when resolving template alias", apiErr.Err, attribute.String("identifier", identifier))
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	env, build, err := a.templateCache.Get(ctx, aliasInfo.TemplateID, tag, teamInfo.Team.ID, clusterID)
	if err != nil {
		visible := aliasInfo.TeamID == teamInfo.Team.ID
		if metadata, mErr := a.templateCache.GetMetadata(ctx, aliasInfo.TemplateID); mErr == nil {
			visible = visible || metadata.Public
		}

		ref := templatecache.TemplateRef{
			Identifier: aliasInfo.MatchedIdentifier,
			Visible:    visible,
		}

		apiErr := ref.APIError(err)
		telemetry.ReportErrorByCode(ctx, apiErr.Code, "error when getting template", apiErr.Err, telemetry.WithTemplateID(aliasInfo.TemplateID))
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	telemetry.ReportEvent(ctx, "Checked team access")

	c.Set("envID", env.TemplateID)
	setTemplateNameMetric(ctx, c, a.featureFlags, env.TemplateID, env.Names)

	sandboxID := InstanceIDPrefix + id.Generate()

	c.Set("instanceID", sandboxID)

	sbxlogger.E(&sbxlogger.SandboxMetadata{
		SandboxID:  sandboxID,
		TemplateID: env.TemplateID,
		TeamID:     teamInfo.Team.ID.String(),
	}).Debug(ctx, "Started creating sandbox")

	alias := firstAlias(env.Aliases)
	telemetry.SetAttributes(ctx,
		telemetry.WithSandboxID(sandboxID),
		telemetry.WithTemplateID(env.TemplateID),
		telemetry.WithBuildID(build.ID.String()),
		attribute.String("env.alias", alias),
		telemetry.WithKernelVersion(build.KernelVersion),
		telemetry.WithFirecrackerVersion(build.FirecrackerVersion),
	)

	autoPause := sharedUtils.DerefOrDefault(body.AutoPause, sandbox.AutoPauseDefault)
	if body.Lifecycle != nil && body.Lifecycle.OnTimeout != nil {
		autoPause = *body.Lifecycle.OnTimeout == api.Pause
	}
	envVars := sharedUtils.DerefOrDefault(body.EnvVars, nil)
	mcp := sharedUtils.DerefOrDefault(body.Mcp, nil)
	metadata := sharedUtils.DerefOrDefault(body.Metadata, nil)
	apiVolumeMounts := sharedUtils.DerefOrDefault(body.VolumeMounts, nil)

	if lifecycleErr := validateLifecycleAliases(body); lifecycleErr != nil {
		a.sendAPIStoreError(c, lifecycleErr.Code, lifecycleErr.ClientMsg)

		return
	}

	timeout := sandbox.SandboxTimeoutDefault
	if body.Timeout != nil {
		timeout = time.Duration(*body.Timeout) * time.Second

		if timeout > time.Duration(teamInfo.Limits.MaxLengthHours)*time.Hour {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Timeout cannot be greater than %d hours", teamInfo.Limits.MaxLengthHours))

			return
		}
	}

	autoResume := buildAutoResumeConfig(body.AutoResume)
	if body.Lifecycle != nil && body.Lifecycle.AutoResume != nil {
		autoResume = buildAutoResumeConfigFromEnabled(*body.Lifecycle.AutoResume)
	}
	if autoResume != nil {
		minAutoResumeTimeout := time.Duration(a.featureFlags.IntFlag(ctx, featureflags.MinAutoResumeTimeoutSeconds)) * time.Second
		autoResume.Timeout = calculateTimeoutSeconds(timeout, minAutoResumeTimeout, teamInfo)
	}
	keepalive, keepaliveErr := buildKeepaliveConfig(body.Lifecycle)
	if keepaliveErr != nil {
		a.sendAPIStoreError(c, keepaliveErr.Code, keepaliveErr.ClientMsg)

		return
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
		if err := validateNetworkConfig(ctx, a.featureFlags, teamInfo.Team.ID, sharedUtils.DerefOrDefault(build.EnvdVersion, ""), n); err != nil {
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
				Rules:            apiRulesToDBRules(n.Rules),
			},
		}

		// Make sure envd seucre access is enforced when public access is disabled,
		// This requirement forces users using newer features to secure sandboxes properly.
		if !sharedUtils.DerefOrDefault(network.Ingress.AllowPublicAccess, types.AllowPublicAccessDefault) && envdAccessToken == nil {
			a.sendAPIStoreError(c, http.StatusBadRequest, "You cannot create a sandbox without public access unless you enable secure envd access via 'secure' flag.")

			return
		}
	}

	sbxVolumeMounts, err := convertAPIVolumesToOrchestratorVolumes(
		ctx, a.sqlcDB, a.featureFlags, teamInfo.ID, apiVolumeMounts, build,
	)
	if err != nil {
		if errors.Is(err, errVolumesNotSupported) || errors.Is(err, errNoEnvdVersion) {
			a.sendAPIStoreError(c, http.StatusBadRequest, err.Error())

			return
		}

		if errors.Is(err, ErrVolumeMountsDisabled) {
			a.sendAPIStoreError(c, http.StatusBadRequest, "Volume mounts are not enabled.")

			return
		}

		var vne InvalidVolumeMountsError
		if errors.As(err, &vne) {
			a.sendAPIStoreError(c, http.StatusBadRequest, vne.Error())

			return
		}

		telemetry.ReportError(ctx, "failed to convert volume mounts", err, telemetry.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "failed to convert volume mounts")

		return
	}

	getSandboxData := func(_ context.Context) (apiorch.SandboxMetadata, *api.APIError) {
		// The data can't be influenced by action on the same sandbox as other operations,
		// so it's safe to reuse the data
		return apiorch.SandboxMetadata{
			Metadata:            metadata,
			EnvVars:             envVars,
			Build:               *build,
			AllowInternetAccess: allowInternetAccess,
			Network:             network,
			Alias:               alias,
			TemplateID:          env.TemplateID,
			BaseTemplateID:      env.TemplateID,
			AutoPause:           autoPause,
			AutoResume:          autoResume,
			Keepalive:           keepalive,
			VolumeMounts:        sbxVolumeMounts,
			EnvdAccessToken:     envdAccessToken,
		}, nil
	}

	sbx, createErr := a.startSandbox(
		ctx,
		sandboxID,
		timeout,
		teamInfo,
		getSandboxData,
		&c.Request.Header,
		false,
		mcp,
	)
	if createErr != nil {
		a.sendAPIStoreError(c, createErr.Code, createErr.ClientMsg)

		return
	}

	if n := body.Network; n != nil && n.Rules != nil && len(*n.Rules) > 0 {
		domains := make([]string, 0, len(*n.Rules))
		for domain := range *n.Rules {
			domains = append(domains, domain)
		}

		a.posthog.CreateAnalyticsTeamEvent(ctx, teamInfo.Team.ID.String(), "sandbox with network transform rules created",
			a.posthog.GetPackageToPosthogProperties(&c.Request.Header).
				Set("sandbox_id", sandboxID).
				Set("domains", domains),
		)
	}

	c.JSON(http.StatusCreated, &sbx)
}

func buildAutoResumeConfig(autoResume *api.SandboxAutoResumeConfig) *types.SandboxAutoResumeConfig {
	if autoResume == nil {
		return nil
	}

	return buildAutoResumeConfigFromEnabled(autoResume.Enabled)
}

func buildAutoResumeConfigFromEnabled(enabled bool) *types.SandboxAutoResumeConfig {
	policy := types.SandboxAutoResumeOff
	if enabled {
		policy = types.SandboxAutoResumeAny
	}

	return &types.SandboxAutoResumeConfig{
		Policy: policy,
	}
}

func validateLifecycleAliases(body api.NewSandbox) *api.APIError {
	if body.Lifecycle == nil {
		return nil
	}

	if body.AutoPause != nil && body.Lifecycle.OnTimeout != nil {
		return &api.APIError{Code: http.StatusBadRequest, ClientMsg: "autoPause and lifecycle.onTimeout cannot both be set"}
	}

	if body.AutoResume != nil && body.Lifecycle.AutoResume != nil {
		return &api.APIError{Code: http.StatusBadRequest, ClientMsg: "autoResume and lifecycle.autoResume cannot both be set"}
	}

	return nil
}

func buildKeepaliveConfig(lifecycle *api.NewSandboxLifecycle) (*types.SandboxKeepaliveConfig, *api.APIError) {
	if lifecycle == nil || lifecycle.Keepalive == nil || lifecycle.Keepalive.Traffic == nil {
		return nil, nil
	}

	timeout := types.SandboxTrafficKeepaliveTimeoutDefault
	if lifecycle.Keepalive.Traffic.Timeout != nil {
		if *lifecycle.Keepalive.Traffic.Timeout < 0 {
			return nil, &api.APIError{Code: http.StatusBadRequest, ClientMsg: "Traffic keepalive timeout cannot be negative"}
		}
		if time.Duration(*lifecycle.Keepalive.Traffic.Timeout)*time.Second <= catalog.TrafficKeepaliveThrottleInterval {
			return nil, &api.APIError{Code: http.StatusBadRequest, ClientMsg: fmt.Sprintf("Traffic keepalive timeout must be greater than %d seconds", int(catalog.TrafficKeepaliveThrottleInterval.Seconds()))}
		}

		timeout = uint64(*lifecycle.Keepalive.Traffic.Timeout)
	}

	return &types.SandboxKeepaliveConfig{
		Traffic: &types.SandboxTrafficKeepaliveConfig{
			Enabled: lifecycle.Keepalive.Traffic.Enabled,
			Timeout: timeout,
		},
	}, nil
}

func dedupeVolumeNames(items []api.SandboxVolumeMount) []string {
	itemsSet := make(map[string]struct{}, len(items))
	for _, item := range items {
		itemsSet[item.Name] = struct{}{}
	}

	results := make([]string, 0, len(itemsSet))
	for name := range itemsSet {
		results = append(results, name)
	}

	return results
}

var ErrVolumeMountsDisabled = errors.New("volume mounts are not enabled")

type featureFlagsClient interface {
	BoolFlag(ctx context.Context, flagName featureflags.BoolFlag, contexts ...ldcontext.Context) bool
}

type InvalidMount struct {
	Index  int
	Reason string
}

type InvalidVolumeMountsError struct {
	InvalidMounts []InvalidMount
}

func (im InvalidVolumeMountsError) Error() string {
	var errs []string

	for _, mount := range im.InvalidMounts {
		errs = append(errs, fmt.Sprintf("\t- volume mount #%d: %s", mount.Index, mount.Reason))
	}

	return fmt.Sprintf("invalid mounts:\n%s", strings.Join(errs, "\n"))
}

var errVolumesNotSupported = errors.New("volumes are not supported")

var errNetworkRulesNotSupported = errors.New("network transform rules are not supported")

var errNoEnvdVersion = errors.New("template must be rebuilt: envd version is not set")

const minEnvdVersionForNetworkRules = "0.5.13"

const minEnvdVersionForVolumes = "0.5.14"

// checkEnvdVersionRequirement returns errNoEnvdVersion when buildVersion is empty, a parse
// error when the version string is invalid, or a wrapped featureErr when the build does not
// meet requiredMinVersion. The caller decides how to convert the returned error into an API
// response so each call-site can produce its own status code / message.
func checkEnvdVersionRequirement(buildVersion, requiredMinVersion string, featureErr error) error {
	if buildVersion == "" {
		return errNoEnvdVersion
	}

	ok, err := sharedUtils.IsGTEVersion(buildVersion, requiredMinVersion)
	if err != nil {
		return fmt.Errorf("invalid envd version %q: %w", buildVersion, err)
	}

	if !ok {
		return fmt.Errorf("%w; template must be rebuilt. Template envd version is %s, must be at least %s", featureErr, buildVersion, requiredMinVersion)
	}

	return nil
}

func convertAPIVolumesToOrchestratorVolumes(ctx context.Context, sqlClient *sqlcdb.Client, featureFlags featureFlagsClient, teamID uuid.UUID, volumeMounts []api.SandboxVolumeMount, env *queries.EnvBuild) ([]*orchestrator.SandboxVolumeMount, error) {
	// are any volumes configured?
	if len(volumeMounts) == 0 {
		return []*orchestrator.SandboxVolumeMount{}, nil // only b/c you should never return (nil, nil)
	}

	// are volumes enabled?
	if !featureFlags.BoolFlag(ctx, featureflags.PersistentVolumesFlag) {
		return nil, ErrVolumeMountsDisabled
	}

	// does your envd version support volumes?
	envdVersion := sharedUtils.DerefOrDefault(env.EnvdVersion, "")
	if err := checkEnvdVersionRequirement(envdVersion, minEnvdVersionForVolumes, errVolumesNotSupported); err != nil {
		return nil, err
	}

	// get volumes from the database
	dbVolumesMap, err := getDBVolumesMap(ctx, sqlClient, teamID, volumeMounts)
	if err != nil {
		return nil, fmt.Errorf("failed to get db volumes map: %w", err)
	}

	invalidVolumeMounts := make([]InvalidMount, 0)
	results := make([]*orchestrator.SandboxVolumeMount, 0, len(volumeMounts))

	usedPaths := make(map[string]struct{})

	for index, v := range volumeMounts {
		actualVolume, ok := dbVolumesMap[v.Name]
		if !ok {
			invalidVolumeMounts = append(invalidVolumeMounts, InvalidMount{Index: index, Reason: fmt.Sprintf("volume '%s' not found", v.Name)})

			continue
		}

		if reason, ok := isValidMountPath(v.Path); !ok {
			invalidVolumeMounts = append(invalidVolumeMounts, InvalidMount{Index: index, Reason: reason})

			continue
		}

		if _, ok := usedPaths[v.Path]; ok {
			invalidVolumeMounts = append(invalidVolumeMounts, InvalidMount{Index: index, Reason: fmt.Sprintf("path '%s' is already used", v.Path)})

			continue
		}
		usedPaths[v.Path] = struct{}{}

		results = append(results, &orchestrator.SandboxVolumeMount{
			Id:   actualVolume.ID.String(),
			Path: v.Path,
			Type: actualVolume.VolumeType,
			Name: actualVolume.Name,
		})
	}

	if len(invalidVolumeMounts) > 0 {
		return nil, InvalidVolumeMountsError{InvalidMounts: invalidVolumeMounts}
	}

	return results, nil
}

func isValidMountPath(path string) (string, bool) {
	if path == "" {
		return "path cannot be empty", false
	}

	if !filepath.IsAbs(path) {
		return "path must be absolute", false
	}

	if filepath.Clean(path) != path {
		return "path must not contain any '.' or '..' components", false
	}

	return "", true
}

func getDBVolumesMap(ctx context.Context, sqlcDB *sqlcdb.Client, teamID uuid.UUID, volumeMounts []api.SandboxVolumeMount) (map[string]queries.Volume, error) {
	dbVolumes, err := sqlcDB.GetVolumesByName(ctx, queries.GetVolumesByNameParams{
		TeamID:      teamID,
		VolumeNames: dedupeVolumeNames(volumeMounts),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get volumes from db: %w", err)
	}

	dbVolumesMap := make(map[string]queries.Volume, len(dbVolumes))
	for _, v := range dbVolumes {
		dbVolumesMap[v.Name] = v
	}

	return dbVolumesMap, nil
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

func setTemplateNameMetric(ctx context.Context, c *gin.Context, ff *featureflags.Client, templateID string, names []string) {
	trackedTemplates := featureflags.GetTrackedTemplatesSet(ctx, ff)

	// Check template ID first
	if _, exists := trackedTemplates[templateID]; exists {
		c.Set(metricTemplateAlias, templateID)

		return
	}

	// Then check names (namespace/alias format when namespaced)
	for _, name := range names {
		if _, exists := trackedTemplates[name]; exists {
			c.Set(metricTemplateAlias, name)

			return
		}
	}

	// Fallback to 'other' if no match of tracked templates found
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

func apiRulesToDBRules(apiRules *map[string][]api.SandboxNetworkRule) map[string][]types.SandboxNetworkRule {
	if apiRules == nil {
		return nil
	}

	dbRules := make(map[string][]types.SandboxNetworkRule, len(*apiRules))
	for domain, rules := range *apiRules {
		dbDomainRules := make([]types.SandboxNetworkRule, 0, len(rules))
		for _, r := range rules {
			dbRule := types.SandboxNetworkRule{}

			if r.Transform != nil {
				dbRule.Transform = &types.SandboxNetworkTransform{
					Headers: sharedUtils.DerefOrDefault(r.Transform.Headers, nil),
				}
			}

			dbDomainRules = append(dbDomainRules, dbRule)
		}

		dbRules[domain] = dbDomainRules
	}

	return dbRules
}

func validateNetworkConfig(ctx context.Context, featureFlags featureFlagsClient, teamID uuid.UUID, envdVersion string, network *api.SandboxNetworkConfig) *api.APIError {
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

	denyOut := sharedUtils.DerefOrDefault(network.DenyOut, nil)
	allowOut := sharedUtils.DerefOrDefault(network.AllowOut, nil)

	if err := validateEgressRules(allowOut, denyOut); err != nil {
		return err
	}

	return validateNetworkRules(ctx, featureFlags, teamID, envdVersion, network.Rules)
}

// validateEgressRules validates egress allow/deny rules:
// - denyOut entries must be valid IPs or CIDRs (not domains)
// - allowOut entries must be valid IPs, CIDRs, or domain names
// - when allowOut contains domains, denyOut must include 0.0.0.0/0
func validateEgressRules(allowOut, denyOut []string) *api.APIError {
	for _, cidr := range denyOut {
		if !sandbox_network.IsSpecifiedIPOrCIDR(cidr) {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("invalid denied CIDR %s", cidr),
				ClientMsg: fmt.Sprintf("invalid denied CIDR %s", cidr),
			}
		}
	}

	if len(allowOut) > 0 {
		allowedAddresses, allowedDomains := sandbox_network.ParseAddressesAndDomains(allowOut)

		for _, addr := range allowedAddresses {
			if !sandbox_network.IsSpecifiedIPOrCIDR(addr) {
				return &api.APIError{
					Code:      http.StatusBadRequest,
					Err:       fmt.Errorf("invalid allowed address %s", addr),
					ClientMsg: fmt.Sprintf("invalid allowed address %s", addr),
				}
			}
		}
		hasBlockAll := slices.Contains(denyOut, sandbox_network.AllInternetTrafficCIDR)

		if len(allowedDomains) > 0 && !hasBlockAll {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       errors.New("allow out contains domains but deny out is missing 0.0.0.0/0 (ALL_TRAFFIC)"),
				ClientMsg: ErrMsgDomainsRequireBlockAll,
			}
		}
	}

	return nil
}

func validateNetworkRules(ctx context.Context, featureFlags featureFlagsClient, teamID uuid.UUID, envdVersion string, rules *map[string][]api.SandboxNetworkRule) *api.APIError {
	if rules == nil {
		return nil
	}

	if !featureFlags.BoolFlag(ctx, featureflags.NetworkTransformRulesFlag, featureflags.TeamContext(teamID.String())) {
		return &api.APIError{
			Code:      http.StatusBadRequest,
			Err:       fmt.Errorf("team %s is not allowed to use network transform rules", teamID),
			ClientMsg: "Network transform rules are not available for your team.",
		}
	}

	if err := checkEnvdVersionRequirement(envdVersion, minEnvdVersionForNetworkRules, errNetworkRulesNotSupported); err != nil {
		if errors.Is(err, errNetworkRulesNotSupported) || errors.Is(err, errNoEnvdVersion) {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       err,
				ClientMsg: err.Error(),
			}
		}

		return &api.APIError{
			Code:      http.StatusInternalServerError,
			Err:       err,
			ClientMsg: "internal error while validating network rules",
		}
	}

	if len(*rules) > maxNetworkRuleDomains {
		return &api.APIError{
			Code:      http.StatusBadRequest,
			Err:       fmt.Errorf("too many rule domains: %d (max %d)", len(*rules), maxNetworkRuleDomains),
			ClientMsg: fmt.Sprintf("Network rules can have at most %d domains.", maxNetworkRuleDomains),
		}
	}

	for domain, domainRules := range *rules {
		if len(domain) == 0 {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       errors.New("rule domain must not be empty"),
				ClientMsg: "Rule domain must not be empty.",
			}
		}

		if len(domain) > maxNetworkRuleDomainLen {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("rule domain %q exceeds max length %d", domain, maxNetworkRuleDomainLen),
				ClientMsg: fmt.Sprintf("Rule domain %q exceeds maximum length of %d characters.", domain, maxNetworkRuleDomainLen),
			}
		}

		if !govalidator.IsDNSName(domain) {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("rule domain %q is not a valid domain", domain),
				ClientMsg: fmt.Sprintf("Rule domain %q is not a valid domain name.", domain),
			}
		}

		if len(domainRules) > maxNetworkRuleTransformsPerDomain {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("domain %q has %d transforms (max %d)", domain, len(domainRules), maxNetworkRuleTransformsPerDomain),
				ClientMsg: fmt.Sprintf("Domain %q can have at most %d transform rule.", domain, maxNetworkRuleTransformsPerDomain),
			}
		}

		for _, rule := range domainRules {
			if rule.Transform == nil {
				continue
			}

			headers := sharedUtils.DerefOrDefault(rule.Transform.Headers, nil)
			if len(headers) > maxNetworkRuleHeadersPerRule {
				return &api.APIError{
					Code:      http.StatusBadRequest,
					Err:       fmt.Errorf("domain %q has %d headers (max %d)", domain, len(headers), maxNetworkRuleHeadersPerRule),
					ClientMsg: fmt.Sprintf("Domain %q can have at most %d headers per rule.", domain, maxNetworkRuleHeadersPerRule),
				}
			}

			for name, value := range headers {
				if len(name) == 0 {
					return &api.APIError{
						Code:      http.StatusBadRequest,
						Err:       fmt.Errorf("header name in rule for domain %q must not be empty", domain),
						ClientMsg: fmt.Sprintf("Header name in rule for domain %q must not be empty.", domain),
					}
				}

				if !httpguts.ValidHeaderFieldName(name) {
					return &api.APIError{
						Code:      http.StatusBadRequest,
						Err:       fmt.Errorf("header name %q in rule for domain %q contains invalid characters", name, domain),
						ClientMsg: fmt.Sprintf("Header name %q in rule for domain %q must contain only valid HTTP token characters.", name, domain),
					}
				}

				if len(name) > maxNetworkRuleHeaderNameLen {
					return &api.APIError{
						Code:      http.StatusBadRequest,
						Err:       fmt.Errorf("header name %q in rule for domain %q exceeds max length %d", name, domain, maxNetworkRuleHeaderNameLen),
						ClientMsg: fmt.Sprintf("Header name %q in rule for domain %q exceeds maximum length of %d characters.", name, domain, maxNetworkRuleHeaderNameLen),
					}
				}

				if !httpguts.ValidHeaderFieldValue(value) {
					return &api.APIError{
						Code:      http.StatusBadRequest,
						Err:       fmt.Errorf("value for header %q in rule for domain %q contains invalid characters", name, domain),
						ClientMsg: fmt.Sprintf("Value for header %q in rule for domain %q contains invalid characters.", name, domain),
					}
				}

				if len(value) > maxNetworkRuleHeaderValueLen {
					return &api.APIError{
						Code:      http.StatusBadRequest,
						Err:       fmt.Errorf("value for header %q in rule for domain %q exceeds max length %d", name, domain, maxNetworkRuleHeaderValueLen),
						ClientMsg: fmt.Sprintf("Value for header %q in rule for domain %q exceeds maximum length of %d characters.", name, domain, maxNetworkRuleHeaderValueLen),
					}
				}
			}
		}
	}

	return nil
}

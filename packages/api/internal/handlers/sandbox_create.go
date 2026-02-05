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

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/net/idna"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	typesteam "github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/middleware/otel/metrics"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
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
)

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

	identifier, tag, err := id.ParseName(body.TemplateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid template reference: %s", err))
		telemetry.ReportError(ctx, "invalid template reference", err)

		return
	}

	clusterID := utils.WithClusterFallback(teamInfo.Team.ClusterID)
	aliasInfo, err := a.templateCache.ResolveAlias(ctx, identifier, teamInfo.Team.Slug)
	if err != nil {
		apiErr := templatecache.ErrorToAPIError(err, identifier)
		telemetry.ReportErrorByCode(ctx, apiErr.Code, "error when resolving template alias", apiErr.Err, attribute.String("identifier", identifier))
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	env, build, err := a.templateCache.Get(ctx, aliasInfo.TemplateID, tag, teamInfo.Team.ID, clusterID)
	if err != nil {
		apiErr := templatecache.ErrorToAPIError(err, aliasInfo.TemplateID)
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
		attribute.String("env.team.id", teamInfo.Team.ID.String()),
		telemetry.WithTemplateID(env.TemplateID),
		attribute.String("env.alias", alias),
		attribute.String("env.kernel.version", build.KernelVersion),
		attribute.String("env.firecracker.version", build.FirecrackerVersion),
	)

	autoPause := sharedUtils.DerefOrDefault(body.AutoPause, sandbox.AutoPauseDefault)
	envVars := sharedUtils.DerefOrDefault(body.EnvVars, nil)
	mcp := sharedUtils.DerefOrDefault(body.Mcp, nil)
	metadata := sharedUtils.DerefOrDefault(body.Metadata, nil)
	apiVolumeMounts := sharedUtils.DerefOrDefault(body.VolumeMounts, nil)

	timeout := sandbox.SandboxTimeoutDefault
	if body.Timeout != nil {
		timeout = time.Duration(*body.Timeout) * time.Second

		if timeout > time.Duration(teamInfo.Limits.MaxLengthHours)*time.Hour {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Timeout cannot be greater than %d hours", teamInfo.Limits.MaxLengthHours))

			return
		}
	}

	cluster, ok := a.clusters.GetClusterById(clusterID)
	if !ok {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Cluster with ID %s not found", clusterID))

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
		// This requirement forces users using newer features to secure sandboxes properly.
		if !sharedUtils.DerefOrDefault(network.Ingress.AllowPublicAccess, types.AllowPublicAccessDefault) && envdAccessToken == nil {
			a.sendAPIStoreError(c, http.StatusBadRequest, "You cannot create a sandbox without public access unless you enable secure envd access via 'secure' flag.")

			return
		}
	}

	sbxVolumeMounts, err := createOrchestratorVolumeMounts(
		ctx, a.sqlcDB, a.featureFlags, cluster, teamInfo.ID, apiVolumeMounts,
	)
	if err != nil {
		if errors.Is(err, ErrVolumeMountsDisabled) {
			a.sendAPIStoreError(c, http.StatusForbidden, "Volume mounts are not enabled.")

			return
		}

		var vne InvalidVolumeMountsError
		if errors.As(err, &vne) {
			a.sendAPIStoreError(c, http.StatusBadRequest, vne.Error())

			return
		}

		var fns InvalidVolumeTypesError
		if errors.As(err, &fns) {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Volume(s) not supported by cluster: %s", strings.Join(fns.VolumeNames, ", ")))

			return
		}

		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Errorf("failed to convert volume mounts: %w", err).Error())

		return
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
		env.TemplateID,
		autoPause,
		envdAccessToken,
		allowInternetAccess,
		network,
		mcp,
		sbxVolumeMounts,
	)
	if createErr != nil {
		a.sendAPIStoreError(c, createErr.Code, createErr.ClientMsg)

		return
	}

	c.JSON(http.StatusCreated, &sbx)
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

func createOrchestratorVolumeMounts(
	ctx context.Context,
	sqlClient *sqlcdb.Client,
	featureFlags featureFlagsClient,
	cluster *clusters.Cluster,
	teamID uuid.UUID,
	volumeMounts []api.SandboxVolumeMount,
) ([]*orchestrator.SandboxVolumeMount, error) {
	if len(volumeMounts) == 0 {
		return []*orchestrator.SandboxVolumeMount{}, nil // only b/c you should never return (nil, nil)
	}

	if !featureFlags.BoolFlag(ctx, featureflags.PersistentVolumesFlag) {
		return nil, ErrVolumeMountsDisabled
	}

	// get volume types from the cluster
	volumeTypesSet, err := getSupportedVolumeTypes(ctx, cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get supported volume types: %w", err)
	}

	// get volumes from the database
	dbVolumesMap, err := getDBVolumesMap(ctx, sqlClient, teamID, volumeMounts, volumeTypesSet)
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

func getSupportedVolumeTypes(ctx context.Context, cluster *clusters.Cluster) (map[string]struct{}, error) {
	volumeTypesSlice, err := cluster.GetResources().GetVolumeTypes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get volume types from cluster: %w", err)
	}

	volumeTypesSet := make(map[string]struct{}, len(volumeTypesSlice))
	for _, vt := range volumeTypesSlice {
		volumeTypesSet[vt] = struct{}{}
	}

	return volumeTypesSet, nil
}

type InvalidVolumeTypesError struct {
	VolumeNames []string
}

func (e InvalidVolumeTypesError) Error() string {
	return fmt.Sprintf("volumes are unsupported by cluster: %s", strings.Join(e.VolumeNames, ", "))
}

func getDBVolumesMap(ctx context.Context, sqlcDB *sqlcdb.Client, teamID uuid.UUID, volumeMounts []api.SandboxVolumeMount, volumeTypesSet map[string]struct{}) (map[string]queries.Volume, error) {
	dbVolumes, err := sqlcDB.GetVolumesByName(ctx, queries.GetVolumesByNameParams{
		TeamID:      teamID,
		VolumeNames: dedupeVolumeNames(volumeMounts),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get volumes from db: %w", err)
	}

	dbVolumesMap := make(map[string]queries.Volume, len(dbVolumes))
	var invalidVolumeNames []string
	for _, v := range dbVolumes {
		if _, ok := volumeTypesSet[v.VolumeType]; !ok {
			invalidVolumeNames = append(invalidVolumeNames, v.Name)

			continue
		}

		dbVolumesMap[v.Name] = v
	}

	if len(invalidVolumeNames) > 0 {
		return nil, InvalidVolumeTypesError{VolumeNames: invalidVolumeNames}
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

	denyOut := sharedUtils.DerefOrDefault(network.DenyOut, nil)
	for _, cidr := range denyOut {
		if !sandbox_network.IsIPOrCIDR(cidr) {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("invalid denied CIDR %s", cidr),
				ClientMsg: fmt.Sprintf("invalid denied CIDR %s", cidr),
			}
		}
	}

	// Validate that allow out rules have corresponding deny out rules
	allowOut := sharedUtils.DerefOrDefault(network.AllowOut, nil)
	if len(allowOut) > 0 {
		_, allowedDomains := sandbox_network.ParseAddressesAndDomains(allowOut)

		// Check if DenyOut contains block-all CIDR
		hasBlockAll := slices.Contains(denyOut, sandbox_network.AllInternetTrafficCIDR)

		// When specifying domains, require block-all CIDR in DenyOut
		// Without this, domain filtering is meaningless (traffic is allowed by default)
		if len(allowedDomains) > 0 && !hasBlockAll {
			return &api.APIError{
				Code:      http.StatusBadRequest,
				Err:       fmt.Errorf("allow out contains domains but deny out is missing 0.0.0.0/0 (ALL_TRAFFIC)"),
				ClientMsg: ErrMsgDomainsRequireBlockAll,
			}
		}
	}

	return nil
}

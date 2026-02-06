package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Masterminds/semver/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	teamtypes "github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/placement"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/pkg/builds"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	feature_flags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	ut "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// buildNetworkConfig constructs the orchestrator network configuration from the input parameters
func buildNetworkConfig(network *types.SandboxNetworkConfig, allowInternetAccess *bool, trafficAccessToken *string) *orchestrator.SandboxNetworkConfig {
	orchNetwork := &orchestrator.SandboxNetworkConfig{
		Egress: &orchestrator.SandboxNetworkEgressConfig{},
		Ingress: &orchestrator.SandboxNetworkIngressConfig{
			TrafficAccessToken: trafficAccessToken,
		},
	}

	// Copy network configuration if provided
	if network != nil && network.Egress != nil {
		// Split allowed addresses into CIDRs/IPs and domains for the orchestrator
		allowedAddresses, allowedDomains := sandbox_network.ParseAddressesAndDomains(network.Egress.AllowedAddresses)

		// If allowed domain is provided, add the default nameserver to the allowed addresses
		// This is to ensure that the sandbox can resolve the domain name to the IP address
		if len(allowedDomains) > 0 {
			allowedAddresses = append(allowedAddresses, sandbox_network.DefaultNameserver)
		}

		orchNetwork.Egress.AllowedCidrs = sandbox_network.AddressStringsToCIDRs(allowedAddresses)
		orchNetwork.Egress.AllowedDomains = allowedDomains

		orchNetwork.Egress.DeniedCidrs = sandbox_network.AddressStringsToCIDRs(network.Egress.DeniedAddresses)
	}

	if network != nil && network.Ingress != nil {
		orchNetwork.Ingress.MaskRequestHost = network.Ingress.MaskRequestHost
	}

	// Handle the case where internet access is explicitly disabled
	// This should be applied after copying the network config to preserve allowed addresses
	if allowInternetAccess != nil && !*allowInternetAccess {
		// Block all internet access - this overrides any other blocked addresses
		orchNetwork.Egress.DeniedCidrs = []string{sandbox_network.AllInternetTrafficCIDR}
	}

	return orchNetwork
}

func getFirecrackerVersion(ctx context.Context, featureFlags *feature_flags.Client, version semver.Version, fallback string) string {
	firecrackerVersions := featureFlags.JSONFlag(ctx, feature_flags.FirecrackerVersions).AsValueMap()
	fcVersion, ok := firecrackerVersions.Get(fmt.Sprintf("v%d.%d", version.Major(), version.Minor())).AsOptionalString().Get()
	if !ok {
		return fallback
	}

	return fcVersion
}

func (o *Orchestrator) CreateSandbox(
	ctx context.Context,
	sandboxID,
	executionID,
	alias string,
	team *teamtypes.Team,
	build queries.EnvBuild,
	metadata map[string]string,
	envVars map[string]string,
	startTime time.Time,
	endTime time.Time,
	timeout time.Duration,
	isResume bool,
	nodeID *string,
	templateID string,
	baseTemplateID string,
	autoPause bool,
	autoResume *types.SandboxAutoResumeConfig,
	envdAuthToken *string,
	allowInternetAccess *bool,
	network *types.SandboxNetworkConfig,
) (sbx sandbox.Sandbox, apiErr *api.APIError) {
	ctx, childSpan := tracer.Start(ctx, "create-sandbox")
	defer childSpan.End()

	// Calculate total concurrent instances including addons
	totalConcurrentInstances := team.Limits.SandboxConcurrency

	// Check if team has reached max instances
	finishStart, waitForStart, err := o.sandboxStore.Reserve(ctx, team.Team.ID, sandboxID, int(totalConcurrentInstances))
	if err != nil {
		var limitErr *sandbox.LimitExceededError

		switch {
		case errors.As(err, &limitErr):
			return sandbox.Sandbox{}, &api.APIError{
				Code: http.StatusTooManyRequests,
				ClientMsg: fmt.Sprintf(
					"you have reached the maximum number of concurrent E2B sandboxes (%d). If you need more, "+
						"please contact us at 'https://e2b.dev/docs/getting-help'", totalConcurrentInstances),
				Err: fmt.Errorf("team '%s' has reached the maximum number of instances (%d)", team.ID, totalConcurrentInstances),
			}
		default:
			logger.L().Error(ctx, "failed to reserve sandbox for team", logger.WithSandboxID(sandboxID), zap.Error(err))

			return sandbox.Sandbox{}, &api.APIError{
				Code:      http.StatusInternalServerError,
				ClientMsg: fmt.Sprintf("Failed to create sandbox: %s", err),
				Err:       err,
			}
		}
	}

	if waitForStart != nil {
		logger.L().Info(ctx, "sandbox is already being started, waiting for it to be ready", logger.WithSandboxID(sandboxID))

		sbx, err = waitForStart(ctx)
		if err != nil {
			logger.L().Warn(ctx, "Error waiting for sandbox to start", zap.Error(err), logger.WithSandboxID(sandboxID))

			var apiErr *api.APIError
			if errors.As(err, &apiErr) {
				return sandbox.Sandbox{}, apiErr
			}

			return sandbox.Sandbox{}, &api.APIError{
				Code:      http.StatusInternalServerError,
				ClientMsg: "Error waiting for sandbox to start",
				Err:       err,
			}
		}

		return sbx, nil
	}

	telemetry.ReportEvent(ctx, "Reserved sandbox for team")
	defer func() {
		// Don't change this handling
		// https://go.dev/play/p/4oy02s7BDMc
		if apiErr != nil {
			finishStart(sbx, apiErr)
		} else {
			finishStart(sbx, nil)
		}
	}()

	fcSemver, err := sandbox.NewVersionInfo(build.FirecrackerVersion)
	if err != nil {
		errMsg := fmt.Errorf("failed to get fcSemver for firecracker fcSemver '%s': %w", build.FirecrackerVersion, err)

		return sandbox.Sandbox{}, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Failed to get build information for the template",
			Err:       errMsg,
		}
	}

	hasHugePages := fcSemver.HasHugePages()
	firecrackerVersion := getFirecrackerVersion(ctx, o.featureFlagsClient, fcSemver.Version(), build.FirecrackerVersion)
	telemetry.ReportEvent(ctx, "Got FC info")

	var sbxDomain *string
	if team.ClusterID != nil {
		cluster, ok := o.clusters.GetClusterById(*team.ClusterID)
		if !ok {
			return sandbox.Sandbox{}, &api.APIError{
				Code:      http.StatusInternalServerError,
				ClientMsg: "Error while looking for sandbox cluster information",
				Err:       fmt.Errorf("cannot access cluster %s associated with team id %s that spawned sandbox %s", *team.ClusterID, team.ID, sandboxID),
			}
		}

		sbxDomain = cluster.SandboxDomain
	}

	var trafficAccessToken *string = nil
	if network != nil && network.Ingress != nil && network.Ingress.AllowPublicAccess != nil && !*network.Ingress.AllowPublicAccess {
		accessToken, err := o.accessTokenGenerator.GenerateTrafficAccessToken(sandboxID)
		if err != nil {
			return sandbox.Sandbox{}, &api.APIError{
				Code:      http.StatusInternalServerError,
				ClientMsg: "Failed to create traffic access token",
				Err:       fmt.Errorf("failed to create traffic access token for sandbox %s: %w", sandboxID, err),
			}
		}

		trafficAccessToken = &accessToken
	}

	sbxNetwork := buildNetworkConfig(network, allowInternetAccess, trafficAccessToken)

	var orchAutoResume *orchestrator.SandboxAutoResumeConfig
	if autoResume != nil {
		orchAutoResume = &orchestrator.SandboxAutoResumeConfig{}
		if autoResume.Policy != nil {
			orchAutoResume.Policy = string(*autoResume.Policy)
		}
	}

	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			BaseTemplateId:      baseTemplateID,
			TemplateId:          templateID,
			Alias:               &alias,
			TeamId:              team.ID.String(),
			BuildId:             build.ID.String(),
			SandboxId:           sandboxID,
			ExecutionId:         executionID,
			KernelVersion:       build.KernelVersion,
			FirecrackerVersion:  firecrackerVersion,
			EnvdVersion:         *build.EnvdVersion,
			Metadata:            metadata,
			EnvVars:             envVars,
			EnvdAccessToken:     envdAuthToken,
			MaxSandboxLength:    team.Limits.MaxLengthHours,
			HugePages:           hasHugePages,
			RamMb:               build.RamMb,
			Vcpu:                build.Vcpu,
			Snapshot:            isResume,
			AutoPause:           autoPause,
			AutoResume:          orchAutoResume,
			AllowInternetAccess: allowInternetAccess,
			Network:             sbxNetwork,
			TotalDiskSizeMb:     ut.FromPtr(build.TotalDiskSizeMb),
		},
		StartTime: timestamppb.New(startTime),
		EndTime:   timestamppb.New(endTime),
	}

	var node *nodemanager.Node

	if isResume && nodeID != nil {
		telemetry.ReportEvent(ctx, "Placing sandbox on the node where the snapshot was taken")

		clusterID := utils.WithClusterFallback(team.ClusterID)
		node = o.GetNode(clusterID, *nodeID)
		if node != nil && node.Status() != api.NodeStatusReady {
			node = nil
		}
	}

	nodeClusterID := utils.WithClusterFallback(team.ClusterID)
	clusterNodes := o.GetClusterNodes(nodeClusterID)

	node, err = placement.PlaceSandbox(ctx, o.placementAlgorithm, clusterNodes, node, sbxRequest, builds.ToMachineInfo(build))
	if err != nil {
		return sandbox.Sandbox{}, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Failed to place sandbox",
			Err:       fmt.Errorf("failed to place sandbox: %w", err),
		}
	}

	// The sandbox was created successfully
	attributes := []attribute.KeyValue{
		attribute.Bool("is_resume", isResume),
		attribute.Bool("node_affinity_requested", nodeID != nil),
		attribute.Bool("node_affinity_success", nodeID != nil && node.ID == *nodeID),
	}
	o.createdSandboxesCounter.Add(ctx, 1, metric.WithAttributes(attributes...))

	// The build should be cached on the node now
	node.InsertBuild(build.ID.String())

	telemetry.SetAttributes(ctx, attribute.String("node.id", node.ID))
	telemetry.ReportEvent(ctx, "Created sandbox")

	// This is to compensate for the time it takes to start the instance
	// Otherwise it could cause the instance to expire before user has a chance to use it
	startTime = time.Now()
	endTime = startTime.Add(timeout)

	sbx = sandbox.NewSandbox(
		sandboxID,
		templateID,
		consts.ClientID,
		&alias,
		executionID,
		team.ID,
		build.ID,
		metadata,
		time.Duration(team.Limits.MaxLengthHours)*time.Hour,
		startTime,
		endTime,
		build.Vcpu,
		*build.TotalDiskSizeMb,
		build.RamMb,
		build.KernelVersion,
		firecrackerVersion,
		*build.EnvdVersion,
		node.ID,
		node.ClusterID,
		autoPause,
		autoResume,
		envdAuthToken,
		allowInternetAccess,
		baseTemplateID,
		sbxDomain,
		network,
		trafficAccessToken,
	)

	err = o.sandboxStore.Add(ctx, sbx, true)
	if err != nil {
		telemetry.ReportError(ctx, "failed to add sandbox to store", err)

		// Clean up the sandbox from the node
		// Copy to a new variable to avoid race conditions
		sbxToRemove := sbx
		go func() {
			killErr := o.removeSandboxFromNode(context.WithoutCancel(ctx), sbxToRemove, sandbox.StateActionKill)
			if killErr != nil {
				logger.L().Error(ctx, "Error pausing sandbox", zap.Error(killErr), logger.WithSandboxID(sbxToRemove.SandboxID))
			}
		}()

		return sandbox.Sandbox{}, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Failed to create sandbox",
			Err:       fmt.Errorf("failed to add sandbox to store: %w", err),
		}
	}

	return sbx, nil
}

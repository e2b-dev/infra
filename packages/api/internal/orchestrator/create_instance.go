package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/placement"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	teamtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/db/pkg/builds"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/clusters"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/fcversion"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	ut "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// SandboxDataFetcher is a callback that fetches sandbox metadata.
// It is called after the concurrency lock is acquired to ensure fresh data.
type SandboxDataFetcher func(ctx context.Context) (SandboxMetadata, *api.APIError)

type SandboxMetadata struct {
	Metadata            map[string]string
	EnvVars             map[string]string
	Build               queries.EnvBuild
	AllowInternetAccess *bool
	Network             *types.SandboxNetworkConfig
	Alias               string
	TemplateID          string
	BaseTemplateID      string
	AutoPause           bool
	AutoResume          *types.SandboxAutoResumeConfig
	Keepalive           *types.SandboxKeepaliveConfig
	Timeout             time.Duration
	VolumeMounts        []*orchestrator.SandboxVolumeMount
	EnvdAccessToken     *string
	NodeID              *string
}

// buildEgressConfig constructs the orchestrator egress configuration from
// allow/deny entry lists. It splits allowed entries into CIDRs and domains,
// and adds the default nameserver when domains are present so the sandbox can
// resolve them.
func buildEgressConfig(allowedEntries, deniedEntries []string) *orchestrator.SandboxNetworkEgressConfig {
	allowedAddresses, allowedDomains := sandbox_network.ParseAddressesAndDomains(allowedEntries)

	if len(allowedDomains) > 0 {
		allowedAddresses = append(allowedAddresses, sandbox_network.DefaultNameserver)
	}

	return &orchestrator.SandboxNetworkEgressConfig{
		AllowedCidrs:   sandbox_network.AddressStringsToCIDRs(allowedAddresses),
		DeniedCidrs:    sandbox_network.AddressStringsToCIDRs(deniedEntries),
		AllowedDomains: allowedDomains,
	}
}

// buildNetworkConfig constructs the orchestrator network configuration from the input parameters
func buildNetworkConfig(network *types.SandboxNetworkConfig, allowInternetAccess *bool, trafficAccessToken *string) *orchestrator.SandboxNetworkConfig {
	orchNetwork := &orchestrator.SandboxNetworkConfig{
		Egress: &orchestrator.SandboxNetworkEgressConfig{},
		Ingress: &orchestrator.SandboxNetworkIngressConfig{
			TrafficAccessToken: trafficAccessToken,
		},
	}

	if network != nil && network.Egress != nil {
		orchNetwork.Egress = buildEgressConfig(network.Egress.AllowedAddresses, network.Egress.DeniedAddresses)
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

func (o *Orchestrator) CreateSandbox(
	ctx context.Context,
	sandboxID,
	executionID string,
	team *teamtypes.Team,
	getSandboxData SandboxDataFetcher,
	startTime time.Time,
	endTime time.Time,
	timeout time.Duration,
	isResume bool,
	creationMeta sandbox.CreationMetadata,
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
						"please visit 'https://e2b.dev/docs/billing'", totalConcurrentInstances),
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

	sbxData, fetchErr := getSandboxData(ctx)
	if fetchErr != nil {
		return sandbox.Sandbox{}, fetchErr
	}
	sbxData.Timeout = timeout

	fcSemver, err := fcversion.New(sbxData.Build.FirecrackerVersion)
	if err != nil {
		errMsg := fmt.Errorf("failed to get fcSemver for firecracker fcSemver '%s': %w", sbxData.Build.FirecrackerVersion, err)

		return sandbox.Sandbox{}, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Failed to get build information for the template",
			Err:       errMsg,
		}
	}

	hasHugePages := fcSemver.HasHugePages()
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
	network := sbxData.Network
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

	sbxNetwork := buildNetworkConfig(network, sbxData.AllowInternetAccess, trafficAccessToken)

	var orchAutoResume *orchestrator.SandboxAutoResumeConfig
	if sbxData.AutoResume != nil {
		orchAutoResume = &orchestrator.SandboxAutoResumeConfig{
			Policy:         string(sbxData.AutoResume.Policy),
			TimeoutSeconds: sbxData.AutoResume.Timeout,
		}
	}

	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			BaseTemplateId:      sbxData.BaseTemplateID,
			TemplateId:          sbxData.TemplateID,
			Alias:               &sbxData.Alias,
			TeamId:              team.ID.String(),
			BuildId:             sbxData.Build.ID.String(),
			SandboxId:           sandboxID,
			ExecutionId:         executionID,
			KernelVersion:       sbxData.Build.KernelVersion,
			FirecrackerVersion:  sbxData.Build.FirecrackerVersion,
			EnvdVersion:         *sbxData.Build.EnvdVersion,
			Metadata:            sbxData.Metadata,
			EnvVars:             sbxData.EnvVars,
			EnvdAccessToken:     sbxData.EnvdAccessToken,
			MaxSandboxLength:    team.Limits.MaxLengthHours,
			HugePages:           hasHugePages,
			RamMb:               sbxData.Build.RamMb,
			Vcpu:                sbxData.Build.Vcpu,
			Snapshot:            isResume,
			AutoPause:           sbxData.AutoPause,
			AutoResume:          orchAutoResume,
			AllowInternetAccess: sbxData.AllowInternetAccess,
			Network:             sbxNetwork,
			TotalDiskSizeMb:     ut.FromPtr(sbxData.Build.TotalDiskSizeMb),
			VolumeMounts:        sbxData.VolumeMounts,
		},
		StartTime: timestamppb.New(startTime),
		EndTime:   timestamppb.New(endTime),
	}

	var node *nodemanager.Node

	if isResume && sbxData.NodeID != nil {
		telemetry.ReportEvent(ctx, "Placing sandbox on the node where the snapshot was taken")

		clusterID := clusters.WithClusterFallback(team.ClusterID)
		node = o.GetNode(clusterID, *sbxData.NodeID)
		if node != nil && node.Status() != api.NodeStatusReady {
			node = nil
		}
	}

	nodeClusterID := clusters.WithClusterFallback(team.ClusterID)
	clusterNodes := o.GetClusterNodes(nodeClusterID)

	labelFilteringEnabled := o.featureFlagsClient.BoolFlag(ctx, featureflags.SandboxLabelBasedSchedulingFlag, featureflags.TeamContext(team.ID.String()), featureflags.SandboxContext(sandboxID))

	node, err = placement.PlaceSandbox(ctx, o.placementAlgorithm, clusterNodes, node, sbxRequest, builds.ToMachineInfo(sbxData.Build), labelFilteringEnabled, team.SandboxSchedulingLabels, sbxData.Keepalive)
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
		attribute.Bool("node_affinity_requested", sbxData.NodeID != nil),
		attribute.Bool("node_affinity_success", sbxData.NodeID != nil && node.ID == *sbxData.NodeID),
	}
	o.createdSandboxesCounter.Add(ctx, 1, metric.WithAttributes(attributes...))

	telemetry.SetAttributes(ctx, attribute.String("node.id", node.ID))
	telemetry.ReportEvent(ctx, "Created sandbox")

	// This is to compensate for the time it takes to start the instance
	// Otherwise it could cause the instance to expire before user has a chance to use it
	startTime = time.Now()
	endTime = startTime.Add(timeout)

	sbx = sandbox.NewSandbox(
		sandboxID,
		sbxData.TemplateID,
		consts.ClientID,
		&sbxData.Alias,
		executionID,
		team.ID,
		sbxData.Build.ID,
		sbxData.Metadata,
		time.Duration(team.Limits.MaxLengthHours)*time.Hour,
		startTime,
		endTime,
		sbxData.Build.Vcpu,
		*sbxData.Build.TotalDiskSizeMb,
		sbxData.Build.RamMb,
		sbxData.Build.KernelVersion,
		sbxData.Build.FirecrackerVersion,
		*sbxData.Build.EnvdVersion,
		node.ID,
		node.ClusterID,
		sbxData.AutoPause,
		sbxData.AutoResume,
		sbxData.Keepalive,
		sbxData.Timeout,
		sbxData.EnvdAccessToken,
		sbxData.AllowInternetAccess,
		sbxData.BaseTemplateID,
		sbxDomain,
		sbxData.Network,
		trafficAccessToken,
		nodemanager.ConvertOrchestratorMountsToDatabaseMounts(sbxData.VolumeMounts),
	)

	err = o.sandboxStore.Add(ctx, sbx, &creationMeta)
	if err != nil {
		telemetry.ReportError(ctx, "failed to add sandbox to store", err)

		// Clean up the sandbox from the node
		// Copy to a new variable to avoid race conditions
		sbxToRemove := sbx
		go func() {
			killErr := o.removeSandboxFromNode(context.WithoutCancel(ctx), sbxToRemove, sandbox.StateActionKill)
			if killErr != nil {
				logger.L().Error(ctx, "Error removing sandbox", zap.Error(killErr), logger.WithSandboxID(sbxToRemove.SandboxID))
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

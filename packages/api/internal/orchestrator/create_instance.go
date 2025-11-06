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
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/placement"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	ut "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (o *Orchestrator) CreateSandbox(
	ctx context.Context,
	sandboxID,
	executionID,
	alias string,
	team *types.Team,
	build queries.EnvBuild,
	metadata map[string]string,
	envVars map[string]string,
	startTime time.Time,
	endTime time.Time,
	timeout time.Duration,
	isResume bool,
	nodeID *string,
	baseTemplateID string,
	autoPause bool,
	envdAuthToken *string,
	allowInternetAccess *bool,
	firewall *orchestrator.SandboxFirewallConfig,
) (sbx sandbox.Sandbox, apiErr *api.APIError) {
	ctx, childSpan := tracer.Start(ctx, "create-sandbox")
	defer childSpan.End()

	// Calculate total concurrent instances including addons
	totalConcurrentInstances := team.Limits.SandboxConcurrency

	// Check if team has reached max instances
	finishStart, waitForStart, err := o.sandboxStore.Reserve(team.Team.ID.String(), sandboxID, totalConcurrentInstances)
	if err != nil {
		var limitErr *sandbox.LimitExceededError

		telemetry.ReportCriticalError(ctx, "failed to reserve sandbox for team", err)

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
			zap.L().Error("failed to reserve sandbox for team", logger.WithSandboxID(sandboxID), zap.Error(err))

			return sandbox.Sandbox{}, &api.APIError{
				Code:      http.StatusInternalServerError,
				ClientMsg: fmt.Sprintf("Failed to create sandbox: %s", err),
				Err:       err,
			}
		}
	}

	if waitForStart != nil {
		zap.L().Info("sandbox is already being started, waiting for it to be ready", logger.WithSandboxID(sandboxID))

		sbx, err = waitForStart(ctx)
		if err != nil {
			zap.L().Warn("Error waiting for sandbox to start", zap.Error(err), logger.WithSandboxID(sandboxID))

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

	features, err := sandbox.NewVersionInfo(build.FirecrackerVersion)
	if err != nil {
		errMsg := fmt.Errorf("failed to get features for firecracker version '%s': %w", build.FirecrackerVersion, err)

		return sandbox.Sandbox{}, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Failed to get build information for the template",
			Err:       errMsg,
		}
	}

	telemetry.ReportEvent(ctx, "Got FC version info")

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

	if allowInternetAccess != nil && !*allowInternetAccess {
		firewall.Egress = firewall.GetEgress()
		firewall.Egress.BlockedCidrs = []string{"0.0.0.0/0"}
	}

	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			BaseTemplateId:      baseTemplateID,
			TemplateId:          build.EnvID,
			Alias:               &alias,
			TeamId:              team.ID.String(),
			BuildId:             build.ID.String(),
			SandboxId:           sandboxID,
			ExecutionId:         executionID,
			KernelVersion:       build.KernelVersion,
			FirecrackerVersion:  build.FirecrackerVersion,
			EnvdVersion:         *build.EnvdVersion,
			Metadata:            metadata,
			EnvVars:             envVars,
			EnvdAccessToken:     envdAuthToken,
			MaxSandboxLength:    team.Limits.MaxLengthHours,
			HugePages:           features.HasHugePages(),
			RamMb:               build.RamMb,
			Vcpu:                build.Vcpu,
			Snapshot:            isResume,
			AutoPause:           autoPause,
			AllowInternetAccess: allowInternetAccess,
			Firewall:            firewall,
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

	algorithm := o.getPlacementAlgorithm(ctx)
	node, err = placement.PlaceSandbox(ctx, algorithm, clusterNodes, node, sbxRequest)
	if err != nil {
		telemetry.ReportError(ctx, "failed to create sandbox", err)

		return sandbox.Sandbox{}, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Failed to create sandbox",
			Err:       fmt.Errorf("failed to get create sandbox: %w", err),
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
		build.EnvID,
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
		build.FirecrackerVersion,
		*build.EnvdVersion,
		node.ID,
		node.ClusterID,
		autoPause,
		envdAuthToken,
		allowInternetAccess,
		baseTemplateID,
		sbxDomain,
		utils.OrchestratorToDBFirewall(firewall),
	)

	o.sandboxStore.Add(ctx, sbx, true)

	return sbx, nil
}

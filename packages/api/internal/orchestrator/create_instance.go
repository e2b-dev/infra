package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/placement"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
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
	team authcache.AuthTeamInfo,
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
) (*api.Sandbox, *api.APIError) {
	ctx, childSpan := o.tracer.Start(ctx, "create-sandbox")
	defer childSpan.End()

	// Check if team has reached max instances
	releaseTeamSandboxReservation, err := o.instanceCache.Reserve(sandboxID, team.Team.ID, team.Tier.ConcurrentInstances)
	if err != nil {
		var limitErr *instance.ErrSandboxLimitExceeded
		var alreadyErr *instance.ErrAlreadyBeingStarted

		telemetry.ReportCriticalError(ctx, "failed to reserve sandbox for team", err)

		switch {
		case errors.As(err, &limitErr):
			return nil, &api.APIError{
				Code: http.StatusTooManyRequests,
				ClientMsg: fmt.Sprintf(
					"you have reached the maximum number of concurrent E2B sandboxes (%d). If you need more, "+
						"please contact us at 'https://e2b.dev/docs/getting-help'", team.Tier.ConcurrentInstances),
				Err: fmt.Errorf("team '%s' has reached the maximum number of instances (%d)", team.Team.ID, team.Tier.ConcurrentInstances),
			}
		case errors.As(err, &alreadyErr):
			zap.L().Warn("sandbox already being started", logger.WithSandboxID(sandboxID), zap.Error(err))
			return nil, &api.APIError{
				Code:      http.StatusConflict,
				ClientMsg: fmt.Sprintf("Sandbox %s is already being started", sandboxID),
				Err:       err,
			}
		default:
			zap.L().Error("failed to reserve sandbox for team", logger.WithSandboxID(sandboxID), zap.Error(err))
			return nil, &api.APIError{
				Code:      http.StatusInternalServerError,
				ClientMsg: fmt.Sprintf("Failed to create sandbox: %s", err),
				Err:       err,
			}
		}
	}

	telemetry.ReportEvent(ctx, "Reserved sandbox for team")
	defer releaseTeamSandboxReservation()

	features, err := sandbox.NewVersionInfo(build.FirecrackerVersion)
	if err != nil {
		errMsg := fmt.Errorf("failed to get features for firecracker version '%s': %w", build.FirecrackerVersion, err)

		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Failed to get build information for the template",
			Err:       errMsg,
		}
	}

	telemetry.ReportEvent(ctx, "Got FC version info")

	var sbxDomain *string
	if team.Team.ClusterID != nil {
		cluster, ok := o.clusters.GetClusterById(*team.Team.ClusterID)
		if !ok {
			return nil, &api.APIError{
				Code:      http.StatusInternalServerError,
				ClientMsg: "Error while looking for sandbox cluster information",
				Err:       fmt.Errorf("cannot access cluster %s associated with team id %s that spawned sandbox %s", *team.Team.ClusterID, team.Team.ID, sandboxID),
			}
		}

		sbxDomain = cluster.SandboxDomain
	}

	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			BaseTemplateId:      baseTemplateID,
			TemplateId:          *build.EnvID,
			Alias:               &alias,
			TeamId:              team.Team.ID.String(),
			BuildId:             build.ID.String(),
			SandboxId:           sandboxID,
			ExecutionId:         executionID,
			KernelVersion:       build.KernelVersion,
			FirecrackerVersion:  build.FirecrackerVersion,
			EnvdVersion:         *build.EnvdVersion,
			Metadata:            metadata,
			EnvVars:             envVars,
			EnvdAccessToken:     envdAuthToken,
			MaxSandboxLength:    team.Tier.MaxLengthHours,
			HugePages:           features.HasHugePages(),
			RamMb:               build.RamMb,
			Vcpu:                build.Vcpu,
			Snapshot:            isResume,
			AutoPause:           autoPause,
			AllowInternetAccess: allowInternetAccess,
			TotalDiskSizeMb:     ut.FromPtr(build.TotalDiskSizeMb),
		},
		StartTime: timestamppb.New(startTime),
		EndTime:   timestamppb.New(endTime),
	}

	var node *nodemanager.Node

	if isResume && nodeID != nil {
		telemetry.ReportEvent(ctx, "Placing sandbox on the node where the snapshot was taken")

		clusterID := uuid.Nil
		if team.Team.ClusterID != nil {
			clusterID = *team.Team.ClusterID
		}

		node = o.GetNode(clusterID, *nodeID)
		if node != nil && node.Status() != api.NodeStatusReady {
			node = nil
		}
	}

	nodeClusterID := uuid.Nil
	if team.Team.ClusterID != nil {
		nodeClusterID = *team.Team.ClusterID
	}

	clusterNodes := o.GetClusterNodes(nodeClusterID)
	ctx, span := o.tracer.Start(ctx, "place-sandbox")
	node, err = placement.PlaceSandbox(ctx, o.tracer, o.placementAlgorithm, clusterNodes, node, sbxRequest)
	if err != nil {
		telemetry.ReportError(ctx, "failed to create sandbox", err)

		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Failed to create sandbox",
			Err:       fmt.Errorf("failed to get create sandbox: %w", err),
		}
	}
	span.End()

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

	sbx := api.Sandbox{
		ClientID:        consts.ClientID,
		SandboxID:       sandboxID,
		TemplateID:      *build.EnvID,
		Alias:           &alias,
		EnvdVersion:     *build.EnvdVersion,
		EnvdAccessToken: envdAuthToken,
		Domain:          sbxDomain,
	}

	// This is to compensate for the time it takes to start the instance
	// Otherwise it could cause the instance to expire before user has a chance to use it
	startTime = time.Now()
	endTime = startTime.Add(timeout)

	instanceInfo := instance.NewInstanceInfo(
		sbx.SandboxID,
		sbx.TemplateID,
		sbx.ClientID,
		sbx.Alias,
		executionID,
		team.Team.ID,
		build.ID,
		metadata,
		time.Duration(team.Tier.MaxLengthHours)*time.Hour,
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
	)

	cacheErr := o.instanceCache.Add(ctx, instanceInfo, true)
	if cacheErr != nil {
		telemetry.ReportError(ctx, "error when adding instance to cache", cacheErr)

		deleted := o.DeleteInstance(ctx, sbx.SandboxID, false)
		if !deleted {
			telemetry.ReportEvent(ctx, "instance wasn't found in cache when deleting")
		}

		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Failed to create sandbox",
			Err:       fmt.Errorf("error when adding instance to cache: %w", cacheErr),
		}
	}

	return &sbx, nil
}
